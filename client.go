package go_dxlink

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"net/url"
	"slices"

	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type filterFunc func(rawOptions []string, mktPrice float64, pctRange float64) []string

type DxLinkClient struct {
	conn           *websocket.Conn
	url            string
	token          string
	mu             sync.RWMutex
	connected      bool
	messageCounter int
	ctx            context.Context
	cancel         context.CancelFunc
	retries        int
	delay          time.Duration
	expBackoff     bool

	// Optional
	dxlog          *slog.Logger
	optionSubs     map[string]*feedData
	underlyingSubs map[string]*feedData
	futuresSubs    map[string]*feedData
	futuresEvent   chan ProcessedFeedData
}

func New(ctx context.Context, url string, token string) *DxLinkClient {
	ctx, cancel := context.WithCancel(ctx)
	return &DxLinkClient{
		url:        url,
		ctx:        ctx,
		cancel:     cancel,
		token:      token,
		retries:    3,
		delay:      1 * time.Second,
		expBackoff: false,
	}
}

func (c *DxLinkClient) WithLogger(logger *slog.Logger) *DxLinkClient {
	c.dxlog = logger
	return c
}

func (c *DxLinkClient) WithOptionSubs(
	symbol string,
	options []string,
	mktPrice float64,
	pctRange float64,
	filter filterFunc,
) *DxLinkClient {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.optionSubs = make(map[string]*feedData)
	filtered := filter(options, mktPrice, pctRange)
	for _, option := range filtered {
		c.optionSubs[option] = NewFeedData().WithGreeks().WithQuote()
	}
	return c
}

func (c *DxLinkClient) WithUnderlying(symbol string) *DxLinkClient {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.underlyingSubs = make(map[string]*feedData)
	c.underlyingSubs[symbol] = NewFeedData().WithTrade()

	return c
}

func (c *DxLinkClient) WithFuture(symbol string) *DxLinkClient {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.futuresSubs = make(map[string]*feedData)
	c.futuresSubs[symbol] = NewFeedData().WithTrade()

	c.futuresEvent = make(chan ProcessedFeedData)

	return c
}

// creates the struct that the "FEED_SUBSCRIPTION" message is based on
func (c *DxLinkClient) underlyingFeedSub() FeedSubscriptionMsg {
	feedSub := FeedSubscriptionMsg{
		Type:    FeedSubscription,
		Channel: 1,
		Reset:   true,
		Add:     []FeedSubItem{},
	}

	for under := range c.underlyingSubs {
		feedSub.Add = append(feedSub.Add, FeedSubItem{Type: "Trade", Symbol: under})
	}
	return feedSub
}

func (c *DxLinkClient) optionFeedSub() FeedSubscriptionMsg {
	feedSub := FeedSubscriptionMsg{
		Type:    FeedSubscription,
		Channel: 3,
		Reset:   true,
		Add:     []FeedSubItem{},
	}
	for opt := range c.optionSubs {
		if len(feedSub.Add) >= 90 {
			break
		}
		feedSub.Add = append(feedSub.Add, FeedSubItem{Type: "Quote", Symbol: opt})
		feedSub.Add = append(feedSub.Add, FeedSubItem{Type: "Greeks", Symbol: opt})
	}
	return feedSub
}

func (c *DxLinkClient) optionFeedIter() iter.Seq[FeedSubscriptionMsg] {
	return func(yield func(FeedSubscriptionMsg) bool) {
		syms := slices.Collect(maps.Keys(c.optionSubs))
		chunks := chunkSlice(syms, 45)

		for _, c := range chunks {
			feedSub := FeedSubscriptionMsg{
				Type:    FeedSubscription,
				Channel: 3,
				Reset:   false,
				Add:     []FeedSubItem{},
			}
			for _, v := range c {
				feedSub.Add = append(feedSub.Add, FeedSubItem{Type: "Quote", Symbol: v})
				feedSub.Add = append(feedSub.Add, FeedSubItem{Type: "Greeks", Symbol: v})
			}
			if !yield(feedSub) {
				return
			}
		}
	}
}

// creates the struct that the "FEED_SUBSCRIPTION" message is based on
func (c *DxLinkClient) futureFeedSub() FeedSubscriptionMsg {
	feedSub := FeedSubscriptionMsg{
		Type:    FeedSubscription,
		Channel: 5,
		Reset:   true,
		Add:     []FeedSubItem{},
	}

	for under := range c.futuresSubs {
		feedSub.Add = append(feedSub.Add, FeedSubItem{Type: "Trade", Symbol: under})
	}
	return feedSub
}

func (c *DxLinkClient) LenOptionSubs() int {
	return len(c.optionSubs)
}

func (c *DxLinkClient) Connect() error {

	if c.connected {
		return fmt.Errorf("client already connected")
	}

	u, err := url.Parse(c.url)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("DXLINK dial error for url: %s : %w", u.String(), err)
	}
	c.conn = conn
	c.connected = true

	// Start message handler
	go c.handleMessages()
	go c.keepAlive()

	setupMsg := SetupMsg{
		Type:                   "SETUP",
		Channel:                0,
		KeepAliveTimeout:       60,
		AcceptKeepAliveTimeout: 60,
		Version:                "0.1-golang",
	}

	err = c.sendMessage(setupMsg)
	if err != nil {
		c.connected = false
		c.conn.Close()
		return fmt.Errorf("failed to send setup message: %w", err)
	}

	return nil
}

func (c *DxLinkClient) reconnect() {
	if !c.connected {
		return
	}
	// Close existing conn if any
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connected = false

	// cancel the message handler
	c.cancel()

	// Try reconnect with backoff
	for retry := 0; retry < c.retries; retry++ {
		slog.Warn("Attempting to reconnect", "try", retry+1, "of max", c.retries)

		backoff := time.Duration(1<<uint(retry)) * time.Second
		time.Sleep(backoff)

		u, err := url.Parse(c.url)
		if err != nil {
			slog.Error("invalid URL during reconnect", "error", err)
			continue
		}

		conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			slog.Error("dial error during reconnect", "url", u.String(), "err", err)
			continue
		}

		c.conn = conn
		c.connected = true

		// Start message handler
		go c.handleMessages()

		setupMsg := SetupMsg{
			Type:                   "SETUP",
			Channel:                0,
			KeepAliveTimeout:       60,
			AcceptKeepAliveTimeout: 60,
			Version:                "0.1-golang",
		}

		err = c.sendMessage(setupMsg)
		if err != nil {
			c.connected = false
			c.conn.Close()
			slog.Error("failed to send setup message during reconnect", "err", err)
			continue
		}
	}
	slog.Error("failed to reconnect")
}

func (c *DxLinkClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slog.Info("closing channel")
	close(c.futuresEvent)

	slog.Info("Closing DxLink WS connection")
	if !c.connected {
		return fmt.Errorf("client not connected")
	}

	c.cancel()

	err := c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil {
		slog.Error("Error sending close message", "err", err)
	}

	c.connected = false
	err = c.conn.Close()
	if err != nil {
		return fmt.Errorf("error closing connection: %w", err)
	}

	return nil
}

func (c *DxLinkClient) sendMessage(msg any) error {
	if c.conn == nil {
		return fmt.Errorf("unable to send message, no connection")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messageCounter++
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("error marshaling message: %w", err)
	}

	n := 100
	msgStr := fmt.Sprintf("%+v", msg)
	if n > len(msgStr) {
		n = len(msgStr)
	}
	c.outputClientMsg("", msgStr[:n])

	err = c.conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return fmt.Errorf("error sending message: %w", err)
	}

	return nil
}

func (c *DxLinkClient) keepAlive() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			time.Sleep(30 * time.Second)
			c.sendMessage(KeepAliveMsg{
				Type:    KeepAlive,
				Channel: 0,
			})
		}
	}
}

// handleMessages reads and processes incoming
func (c *DxLinkClient) handleMessages() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			if c.conn == nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			_, message, err := c.conn.ReadMessage()
			if err != nil {
				slog.Error("Error reading DxLink message", "err", err)
				c.reconnect()
			}

			go c.processMessage(message)
		}
	}
}

func (c *DxLinkClient) processMessage(message []byte) {
	var msgMap map[string]any
	if err := json.Unmarshal(message, &msgMap); err != nil {
		slog.Error("Error unmarshaling message", "err", err)
		return
	}

	msgType, ok := msgMap["type"].(string)
	if !ok {
		slog.Info("Unknown message format", "msg", string(message))
		return
	}

	switch msgType {
	case string(Setup):
		resp := SetupMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			slog.Error("unable to unmarshal setup msg")
			return
		}
		c.outputServerMsg("", resp)
	case string(AuthState):
		resp := AuthStateMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			slog.Error("unable to unmarshal auth state msg")
			return
		}
		c.outputServerMsg("", resp)
		if resp.State == "UNAUTHORIZED" {
			authMsg := AuthMsg{
				Type:    Auth,
				Channel: 0,
				Token:   c.token,
			}
			c.sendMessage(authMsg)
		} else if resp.State == "AUTHORIZED" {
			// setup a channel for underlying (indices, equities) and options
			chanReq := ChannelReqRespMsg{
				Type:    ChannelRequest,
				Channel: 1,
				Service: FeedService,
				Parameters: Parameters{
					Contract: ChannelAuto,
				},
			}
			c.sendMessage(chanReq)

			// setup a channel for options
			chanReq = ChannelReqRespMsg{
				Type:    ChannelRequest,
				Channel: 3,
				Service: FeedService,
				Parameters: Parameters{
					Contract: ChannelAuto,
				},
			}
			c.sendMessage(chanReq)

			// setup a channel for futures
			chanReq = ChannelReqRespMsg{
				Type:    ChannelRequest,
				Channel: 5,
				Service: FeedService,
				Parameters: Parameters{
					Contract: ChannelAuto,
				},
			}
			c.sendMessage(chanReq)
		}
	case string(ChannelOpened):
		resp := ChannelReqRespMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			slog.Error("unable to unmarshal channel open msg")
			return
		}
		c.outputServerMsg("", resp)
		// TODO: add eventTime to quote for age validation
		var feedSetup FeedSetupMsg
		switch resp.Channel {
		case 1:
			// UNDERLYING
			feedSetup = FeedSetupMsg{
				Type:                    FeedSetup,
				Channel:                 1,
				AcceptAggregationPeriod: 60,
				AcceptDataFormat:        CompactFormat,
				AcceptEventFields: FeedEventFields{
					//Quote: []string{"eventType", "eventSymbol", "bidPrice", "askPrice"},
					Trade: []string{"eventType", "eventSymbol", "price", "size"},
					//Candle: []string{"eventType", "eventSymbol", "time", "open", "high", "low", "close", "volume", "impVolatility", "openInterest"},
				},
			}
		case 3:
			// OPTIONS
			feedSetup = FeedSetupMsg{
				Type:                    FeedSetup,
				Channel:                 3,
				AcceptAggregationPeriod: 60,
				AcceptDataFormat:        CompactFormat,
				AcceptEventFields: FeedEventFields{
					Quote:  []string{"eventType", "eventSymbol", "bidPrice", "askPrice"},
					Greeks: []string{"eventType", "eventSymbol", "price", "volatility", "delta", "gamma", "theta", "rho", "vega"},
				},
			}
		case 5:
			// FUTURES
			feedSetup = FeedSetupMsg{
				Type:                    FeedSetup,
				Channel:                 5,
				AcceptAggregationPeriod: 60,
				AcceptDataFormat:        CompactFormat,
				AcceptEventFields: FeedEventFields{
					Trade: []string{"eventType", "eventSymbol", "price", "size"},
					//Candle: []string{"eventType", "eventSymbol", "time", "open", "high", "low", "close", "volume", "impVolatility", "openInterest"},
				},
			}
		}
		c.sendMessage(feedSetup)
	case string(FeedConfig):
		resp := FeedConfigMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			slog.Error("unable to unmarshal feed config msg", "err", err)
			fmt.Printf("%s\n\n", string(message))
			return
		}
		c.outputServerMsg("", resp)
		var feedSub FeedSubscriptionMsg
		switch resp.Channel {
		case 1:
			feedSub = c.underlyingFeedSub()
			c.sendMessage(feedSub)
		case 3:
			//feedSub = c.optionFeedSub()
			for m := range c.optionFeedIter() {
				c.sendMessage(m)
			}
		case 5:
			feedSub = c.futureFeedSub()
			c.sendMessage(feedSub)
		}
	case string(FeedData):
		resp := FeedDataMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			slog.Error("unable to unmarshal feed data msg", "err", err)
			fmt.Printf("%s\n", string(message))
			return
		}

		c.mu.Lock()
		defer c.mu.Unlock()
		switch resp.Channel {
		case 1:
			if len(resp.Data.Trades) > 0 {
				c.outputServerMsg("trades rec'd", resp.Data.Trades[0], "trades", len(resp.Data.Trades))
				for _, trade := range resp.Data.Trades {
					if _, ok := c.underlyingSubs[trade.Symbol]; !ok {
						c.underlyingSubs[trade.Symbol] = NewFeedData().WithTrade()
					}
					c.underlyingSubs[trade.Symbol].Trade = trade
				}
			}
		case 3:
			if len(resp.Data.Quotes) > 0 {
				c.outputServerMsg("quotes rec'd", resp.Data.Quotes[0], "size", len(resp.Data.Quotes))
				for _, quote := range resp.Data.Quotes {
					c.optionSubs[quote.Symbol].Quote = quote
				}
			}
			if len(resp.Data.Greeks) > 0 {
				c.outputServerMsg("greeks rec'd", resp.Data.Greeks[0], "size", len(resp.Data.Greeks))
				for _, greek := range resp.Data.Greeks {
					c.optionSubs[greek.Symbol].Greek = greek
				}
			}
		case 5:
			if len(resp.Data.Trades) > 0 {
				c.outputServerMsg("futures trades rec'd", resp.Data.Trades[0], "trades", len(resp.Data.Trades))
				for _, trade := range resp.Data.Trades {
					if _, ok := c.futuresSubs[trade.Symbol]; !ok {
						c.futuresSubs[trade.Symbol] = NewFeedData().WithTrade()
						c.futuresSubs[trade.Symbol].Trade = trade
					}
					c.futuresSubs[trade.Symbol].Trade = trade
				}
			}
			c.produceFuturesData(resp.Data)
		}
	case string(Error):
		resp := ErrorMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			c.outputMsg("unable to unmarshal error msg", "err", err)
			return
		}
		c.outputServerMsg("", resp)
	case string(KeepAlive):
		resp := KeepAliveMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			c.outputMsg("unable to unmarshal keepalive msg", "err", err)
			return
		}
		c.outputServerMsg("", resp)
	default:
		c.outputMsg("Unknown message type", "msg", string(message))
	}
}

func (c *DxLinkClient) produceFuturesData(data ProcessedFeedData) {
	select {
	case c.futuresEvent <- data:
		// successfully sent
	default:
		select {
		case <-c.futuresEvent:
			// drain an old value
		default:
		}
		// try sending again
		select {
		case c.futuresEvent <- data:
		default:
			// still full, value dropped
		}
	}
}

func (c *DxLinkClient) FuturesEventProducer() <-chan ProcessedFeedData {
	return c.futuresEvent
}

func (c *DxLinkClient) ResetData() {
	clear(c.optionSubs)
	clear(c.underlyingSubs)
	clear(c.futuresSubs)
}

func chunkSlice[T any](slice []T, chunkSize int) [][]T {
	var chunks [][]T
	if chunkSize <= 0 {
		return chunks // Return empty if chunk size is invalid
	}

	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		// Ensure the end index does not exceed the slice's length
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

func (c *DxLinkClient) outputServerMsg(args ...any) {
	c.outputMsg("SERVER <-", args...)
}

func (c *DxLinkClient) outputClientMsg(args ...any) {
	c.outputMsg("CLIENT ->", args...)
}

func (c *DxLinkClient) outputMsg(prefix string, args ...any) {
	if c.dxlog != nil {
		c.dxlog.Info(prefix, args...)
	}
}

func pprint(msg any) {
	fd, _ := json.MarshalIndent(msg, "", "  ")
	fmt.Println(string(fd))
}
