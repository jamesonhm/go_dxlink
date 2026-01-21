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

// ChannelConfig holds config for a specific channel
type ChannelConfig struct {
	Channel           int
	Contract          ChannelContract
	AggregationPeriod int
	DataFormat        FeedDataFormat
	EventFields       map[string][]string  // event type -> field names
	Symbols           map[string]*feedData // symbol -> feedData
	pendingSubs       []FeedSubItem        // Items not yet sent to server
}

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
	dxlog *slog.Logger

	//Channel-based subscription management
	channels      map[int]*ChannelConfig
	futuresEvent  chan ProcessedFeedData
	dataCallbacks map[int]func(ProcessedFeedData) // Channel -> callback
	//optionSubs     map[string]*feedData
	//underlyingSubs map[string]*feedData
	//futuresSubs    map[string]*feedData
}

func New(ctx context.Context, url string, token string) *DxLinkClient {
	ctx, cancel := context.WithCancel(ctx)
	return &DxLinkClient{
		url:           url,
		ctx:           ctx,
		cancel:        cancel,
		token:         token,
		retries:       3,
		delay:         1 * time.Second,
		expBackoff:    false,
		channels:      make(map[int]*ChannelConfig),
		dataCallbacks: make(map[int]func(ProcessedFeedData)),
	}
}

// ----- Builder Methods -----
func (c *DxLinkClient) WithLogger(logger *slog.Logger) *DxLinkClient {
	c.dxlog = logger
	return c
}

func (c *DxLinkClient) WithSubscription(config ChannelConfig) *DxLinkClient {
	c.mu.Lock()
	defer c.mu.Unlock()

	if config.Symbols == nil {
		config.Symbols = make(map[string]*feedData)
	}

	// Set defaults if not provided
	if config.AggregationPeriod == 0 {
		config.AggregationPeriod = 60
	}

	c.channels[config.Channel] = &config
	return c
}

func (c *DxLinkClient) AddSymbols(channel int, eventTypes []string, symbols ...string) *DxLinkClient {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Get or create channel config
	channelCfg, exists := c.channels[channel]
	if !exists {
		// Auto-create channel with defaults
		channelCfg = &ChannelConfig{
			Channel:           channel,
			Contract:          ChannelAuto,
			AggregationPeriod: 60,
			DataFormat:        CompactFormat,
			EventFields:       make(map[string][]string),
			Symbols:           make(map[string]*feedData),
		}
		c.channels[channel] = channelCfg
	}

	// Add default event fields if not already configured
	for _, eventType := range eventTypes {
		if _, exists := channelCfg.EventFields[eventType]; !exists {
			channelCfg.EventFields[eventType] = getDefaultEventFields(eventType)
		}
	}

	// Add Symbols
	for _, symbol := range symbols {
		feedData := NewFeedData()

		for _, eventType := range eventTypes {
			switch eventType {
			case "Quote":
				feedData.WithQuote()
			case "Trade":
				feedData.WithTrade()
			case "Greeks":
				feedData.WithGreeks()
			case "Candle":
				feedData.WithCandles()
			}
		}

		channelCfg.Symbols[symbol] = feedData

		// Add to pending subscriptions
		for _, eventType := range eventTypes {
			channelCfg.pendingSubs = append(channelCfg.pendingSubs, FeedSubItem{
				Type:   eventType,
				Symbol: symbol,
			})
		}
	}

	return c
}

// Convenience methods for common configs
func (c *DxLinkClient) WithEquities(symbols ...string) *DxLinkClient {
	//c.underlyingSubs = make(map[string]*feedData)
	//c.underlyingSubs[symbol] = NewFeedData().WithTrade()
	return c.AddSymbols(1, []string{"Trade"}, symbols...)
}

func (c *DxLinkClient) WithOptions(symbols ...string) *DxLinkClient {
	return c.AddSymbols(3, []string{"Quote", "Greeks"}, symbols...)
}

func (c *DxLinkClient) WithFuture(symbols ...string) *DxLinkClient {
	c.futuresEvent = make(chan ProcessedFeedData)

	return c.AddSymbols(5, []string{"Trade"}, symbols...)
}

// WithDataCallback sets a callback for data recieved on a specific channel
func (c *DxLinkClient) WithDataCallback(channel int, callback func(ProcessedFeedData)) *DxLinkClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataCallbacks[channel] = callback
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

func (c *DxLinkClient) LenSubscriptions(channel int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if channelCfg, exists := c.channels[channel]; exists {
		return len(channelCfg.Symbols)
	}
	return 0
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

		c.outputMsg("Reconnection successful")
		return
	}
	slog.Error("failed to reconnect after all retries")
}

func (c *DxLinkClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	slog.Info("closing output channels")
	if c.futuresEvent != nil {
		close(c.futuresEvent)
	}

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
				// NEW:
				return
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
			c.mu.Lock()
			for channelID, channelCfg := range c.channels {
				chanReq := ChannelReqRespMsg{
					Type:    ChannelRequest,
					Channel: channelID,
					Service: FeedService,
					Parameters: Parameters{
						Contract: channelCfg.Contract,
					},
				}
				c.sendMessage(chanReq)
			}
			c.mu.Unlock()
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
		c.mu.RLock()
		channelCfg, exists := c.channels[resp.Channel]
		c.mu.RUnlock()

		if !exists {
			c.outputMsg("recieved CHANNEL_OPENED for unconfigured channel")
			return
		}

		// Send FEED_SETUP for this channel
		feedSetup := FeedSetupMsg{
			Type:                    FeedSetup,
			Channel:                 resp.Channel,
			AcceptAggregationPeriod: channelCfg.AggregationPeriod,
			AcceptDataFormat:        CompactFormat,
			AcceptEventFields:       convertToFeedEventFields(channelCfg.EventFields),
		}
		c.sendMessage(feedSetup)
		//feedSetup = FeedSetupMsg{
		//	Type:                    FeedSetup,
		//	Channel:                 1,
		//	AcceptAggregationPeriod: 60,
		//	AcceptDataFormat:        CompactFormat,
		//	AcceptEventFields: FeedEventFields{
		//		//Quote: []string{"eventType", "eventSymbol", "bidPrice", "askPrice"},
		//		Trade: []string{"eventType", "eventSymbol", "price", "size"},
		//		//Candle: []string{"eventType", "eventSymbol", "time", "open", "high", "low", "close", "volume", "impVolatility", "openInterest"},
		//	},
		//}
	case string(FeedConfig):
		resp := FeedConfigMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			slog.Error("unable to unmarshal feed config msg", "err", err)
			fmt.Printf("%s\n\n", string(message))
			return
		}
		c.outputServerMsg("", resp)

		c.mu.RLock()
		channelCfg, exists := c.channels[resp.Channel]
		c.mu.RUnlock()

		if !exists || len(channelCfg.pendingSubs) == 0 {
			return
		}
		// Send subscription message in chunks
		c.sendSubscriptionsInChunks(resp.Channel, channelCfg.pendingSubs)

		// Clear pending subscriptions
		c.mu.Lock()
		channelCfg.pendingSubs = nil
		c.mu.Unlock()

		//switch resp.Channel {
		//case 1:
		//	feedSub = c.underlyingFeedSub()
		//	c.sendMessage(feedSub)
		//case 3:
		//	//feedSub = c.optionFeedSub()
		//	for m := range c.optionFeedIter() {
		//		c.sendMessage(m)
		//	}
		//}
	case string(FeedData):
		resp := FeedDataMsg{}
		err := json.Unmarshal(message, &resp)
		if err != nil {
			slog.Error("unable to unmarshal feed data msg", "err", err)
			fmt.Printf("%s\n", string(message))
			return
		}

		c.processFeedData(resp.Channel, resp.Data)

		//c.mu.Lock()
		//defer c.mu.Unlock()
		//switch resp.Channel {
		//case 1:
		//	if len(resp.Data.Trades) > 0 {
		//		c.outputServerMsg("trades rec'd", resp.Data.Trades[0], "trades", len(resp.Data.Trades))
		//		for _, trade := range resp.Data.Trades {
		//			if _, ok := c.underlyingSubs[trade.Symbol]; !ok {
		//				c.underlyingSubs[trade.Symbol] = NewFeedData().WithTrade()
		//			}
		//			c.underlyingSubs[trade.Symbol].Trade = trade
		//		}
		//	}
		//case 3:
		//	if len(resp.Data.Quotes) > 0 {
		//		c.outputServerMsg("quotes rec'd", resp.Data.Quotes[0], "size", len(resp.Data.Quotes))
		//		for _, quote := range resp.Data.Quotes {
		//			c.optionSubs[quote.Symbol].Quote = quote
		//		}
		//	}
		//	if len(resp.Data.Greeks) > 0 {
		//		c.outputServerMsg("greeks rec'd", resp.Data.Greeks[0], "size", len(resp.Data.Greeks))
		//		for _, greek := range resp.Data.Greeks {
		//			c.optionSubs[greek.Symbol].Greek = greek
		//		}
		//	}
		//case 5:
		//	if len(resp.Data.Trades) > 0 {
		//		c.outputServerMsg("futures trades rec'd", resp.Data.Trades[0], "trades", len(resp.Data.Trades))
		//		for _, trade := range resp.Data.Trades {
		//			if _, ok := c.futuresSubs[trade.Symbol]; !ok {
		//				c.futuresSubs[trade.Symbol] = NewFeedData().WithTrade()
		//				c.futuresSubs[trade.Symbol].Trade = trade
		//			}
		//			c.futuresSubs[trade.Symbol].Trade = trade
		//		}
		//	}
		//	c.produceFuturesData(resp.Data)
		//}
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

func (c *DxLinkClient) processFeedData(channel int, data ProcessedFeedData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	channelCfg, exists := c.channels[channel]
	if !exists {
		return
	}

	// Update local subscription data
	if len(data.Trades) > 0 {
		c.outputServerMsg("trades rec'd", data.Trades[0], "count", len(data.Trades))
		for _, trade := range data.Trades {
			if feedData, ok := channelCfg.Symbols[trade.Symbol]; ok {
				feedData.Trade = trade
			}
		}
	}

	if len(data.Quotes) > 0 {
		c.outputServerMsg("quotes rec'd", data.Quotes[0], "count", len(data.Quotes))
		for _, quote := range data.Quotes {
			if feedData, ok := channelCfg.Symbols[quote.Symbol]; ok {
				feedData.Quote = quote
			}
		}
	}

	if len(data.Greeks) > 0 {
		c.outputServerMsg("greeks rec'd", data.Greeks[0], "count", len(data.Greeks))
		for _, greek := range data.Greeks {
			if feedData, ok := channelCfg.Symbols[greek.Symbol]; ok {
				feedData.Greek = greek
			}
		}
	}

	if len(data.Candles) > 0 {
		c.outputServerMsg("candles rec'd", data.Candles[0], "count", len(data.Candles))
		for _, candle := range data.Candles {
			if feedData, ok := channelCfg.Symbols[candle.Symbol]; ok {
				if feedData.Candles == nil {
					feedData.Candles = make(map[int64]CandleEvent)
				}
				if candle.Time != nil {
					feedData.Candles[int64(*candle.Time)] = candle
				}
			}
		}
	}

	// Call channel-specific callback if registered
	if callback, exists := c.dataCallbacks[channel]; exists {
		callback(data)
	}

	// Output channel producers
	if channel == 5 && c.futuresEvent != nil {
		c.produceData(c.futuresEvent, data)
	}
}

func (c *DxLinkClient) produceData(ch chan ProcessedFeedData, data ProcessedFeedData) {
	select {
	case ch <- data:
		// successfully sent
	default:
		select {
		case <-ch:
			// drain an old value
		default:
		}
		// try sending again
		select {
		case ch <- data:
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

func getDefaultEventFields(eventType string) []string {
	switch eventType {
	case "Quote":
		return []string{"eventType", "eventSymbol", "bidPrice", "askPrice"}
	case "Trade":
		return []string{"eventType", "eventSymbol", "price", "size"}
	case "Greeks":
		return []string{"eventType", "eventSymbol", "price", "volatility", "delta", "gamma", "theta", "rho", "vega"}
	case "Candle":
		return []string{"eventType", "eventSymbol", "time", "open", "high", "low", "close", "volume", "impVolatility", "openInterest"}
	default:
		return []string{}
	}
}

func pprint(msg any) {
	fd, _ := json.MarshalIndent(msg, "", "  ")
	fmt.Println(string(fd))
}
