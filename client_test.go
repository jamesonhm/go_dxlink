package go_dxlink

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock WebSocket server for testing
type mockWSServer struct {
	server   *httptest.Server
	upgrader websocket.Upgrader
	handler  func(*websocket.Conn)
}

func newMockWSServer(handler func(*websocket.Conn)) *mockWSServer {
	mws := &mockWSServer{
		upgrader: websocket.Upgrader{},
		handler:  handler,
	}

	mws.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := mws.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mws.handler(conn)
	}))

	return mws
}

func (mws *mockWSServer) URL() string {
	return "ws" + strings.TrimPrefix(mws.server.URL, "http")
}

func (mws *mockWSServer) Close() {
	mws.server.Close()
}

func TestNew(t *testing.T) {
	ctx := context.Background()
	url := "ws://localhost:8080"
	token := "test-token"

	client := New(ctx, url, token)

	assert.NotNil(t, client)
	assert.Equal(t, url, client.url)
	assert.Equal(t, token, client.token)
	assert.Equal(t, 3, client.retries)
	assert.Equal(t, 1*time.Second, client.delay)
	assert.False(t, client.expBackoff)
	assert.NotNil(t, client.channels)
	assert.NotNil(t, client.dataCallbacks)
}

func TestWithLogger(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	client := New(ctx, "ws://test", "token").WithLogger(logger)

	assert.Equal(t, logger, client.dxlog)
}

func TestWithChannel(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	config := ChannelConfig{
		Channel:           1,
		contract:          ChannelAuto,
		AggregationPeriod: 30,
		DataFormat:        FullFormat,
		EventFields: map[string][]string{
			"Trade": {"eventType", "eventSymbol", "price"},
		},
	}

	client.WithChannel(config)

	assert.Len(t, client.channels, 1)
	assert.NotNil(t, client.channels[1])
	assert.Equal(t, 30, client.channels[1].AggregationPeriod)
	assert.Equal(t, CompactFormat, client.channels[1].DataFormat)
	assert.Equal(t, ChannelAuto, client.channels[1].contract)
}

func TestWithChannel_Defaults(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	config := ChannelConfig{
		Channel: 1,
	}

	client.WithChannel(config)

	channelCfg := client.channels[1]
	assert.Equal(t, 60, channelCfg.AggregationPeriod)
	assert.Equal(t, CompactFormat, channelCfg.DataFormat)
	assert.Equal(t, ChannelAuto, channelCfg.contract)
	assert.NotNil(t, channelCfg.Symbols)
}

func TestAddSymbols(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY", "QQQ")

	assert.Len(t, client.channels, 1)
	channelCfg := client.channels[1]
	assert.Len(t, channelCfg.Symbols, 2)
	assert.NotNil(t, channelCfg.Symbols["SPY"])
	assert.NotNil(t, channelCfg.Symbols["QQQ"])
	assert.NotNil(t, channelCfg.Symbols["SPY"].Trade.Price)
}

func TestAddSymbols_MultipleEventTypes(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(3, []string{"Quote", "Greeks"}, ".SPY240115C450")

	channelCfg := client.channels[3]
	feedData := channelCfg.Symbols[".SPY240115C450"]

	assert.NotNil(t, feedData.Quote.BidPrice)
	assert.NotNil(t, feedData.Quote.AskPrice)
	assert.NotNil(t, feedData.Greek.Delta)
	assert.NotNil(t, feedData.Greek.Gamma)
}

func TestAddSymbols_PendingSubscriptions(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade", "Quote"}, "SPY")

	channelCfg := client.channels[1]
	assert.Len(t, channelCfg.pendingSubs, 2)

	assert.Equal(t, "Trade", channelCfg.pendingSubs[0].Type)
	assert.Equal(t, "SPY", channelCfg.pendingSubs[0].Symbol)
	assert.Equal(t, "Quote", channelCfg.pendingSubs[1].Type)
	assert.Equal(t, "SPY", channelCfg.pendingSubs[1].Symbol)
}

func TestWithEquities(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token").WithEquities("SPY", "QQQ", "AAPL")

	assert.Len(t, client.channels, 1)
	channelCfg := client.channels[1]
	assert.Len(t, channelCfg.Symbols, 3)
	assert.Contains(t, channelCfg.EventFields, "Trade")
}

func TestWithOptions(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token").WithOptions(".SPY240115C450", ".SPY240115P450")

	assert.Len(t, client.channels, 1)
	channelCfg := client.channels[3]
	assert.Len(t, channelCfg.Symbols, 2)
	assert.Contains(t, channelCfg.EventFields, "Quote")
	assert.Contains(t, channelCfg.EventFields, "Greeks")
}

func TestWithFutures(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token").WithFutures("/ES", "/NQ")

	assert.Len(t, client.channels, 1)
	channelCfg := client.channels[5]
	assert.Len(t, channelCfg.Symbols, 2)
	assert.NotNil(t, client.futuresEvent)
}

func TestWithDataCallback(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	callbackCalled := false
	callback := func(data ProcessedFeedData) {
		callbackCalled = true
	}

	client.WithDataCallback(3, callback)

	assert.Len(t, client.dataCallbacks, 1)
	assert.NotNil(t, client.dataCallbacks[3])

	// Test callback is called
	client.dataCallbacks[3](ProcessedFeedData{})
	assert.True(t, callbackCalled)
}

func TestLenSubscriptions(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	assert.Equal(t, 0, client.LenSubscriptions(1))

	client.AddSymbols(1, []string{"Trade"}, "SPY", "QQQ", "AAPL")

	assert.Equal(t, 3, client.LenSubscriptions(1))
	assert.Equal(t, 0, client.LenSubscriptions(2))
}

func TestResetData(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY", "QQQ")
	client.AddSymbols(3, []string{"Quote"}, ".SPY240115C450")

	assert.Len(t, client.channels[1].Symbols, 2)
	assert.Len(t, client.channels[3].Symbols, 1)

	client.ResetData()

	assert.Len(t, client.channels[1].Symbols, 0)
	assert.Len(t, client.channels[3].Symbols, 0)
}

func TestConnect(t *testing.T) {
	receivedSetup := false
	server := newMockWSServer(func(conn *websocket.Conn) {
		// Read SETUP message
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var setupMsg SetupMsg
		if err := json.Unmarshal(msg, &setupMsg); err != nil {
			return
		}

		if setupMsg.Type == "SETUP" {
			receivedSetup = true
		}

		// Send SETUP response
		response := SetupMsg{
			Type:                   "SETUP",
			Channel:                0,
			KeepAliveTimeout:       60,
			AcceptKeepAliveTimeout: 60,
			Version:                "0.1-test",
		}
		conn.WriteJSON(response)

		// Keep connection alive for test
		time.Sleep(100 * time.Millisecond)
	})
	defer server.Close()

	ctx := context.Background()
	client := New(ctx, server.URL(), "test-token")

	err := client.Connect()
	require.NoError(t, err)
	assert.True(t, client.connected)

	time.Sleep(50 * time.Millisecond)
	assert.True(t, receivedSetup)

	client.Close()
}

func TestConnect_AlreadyConnected(t *testing.T) {
	server := newMockWSServer(func(conn *websocket.Conn) {
		time.Sleep(100 * time.Millisecond)
	})
	defer server.Close()

	ctx := context.Background()
	client := New(ctx, server.URL(), "test-token")

	err := client.Connect()
	require.NoError(t, err)

	err = client.Connect()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already connected")

	client.Close()
}

func TestProcessFeedData_Trades(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	price := 450.0
	size := 100.0
	data := ProcessedFeedData{
		Trades: []TradeEvent{
			{
				EventType: "Trade",
				Symbol:    "SPY",
				Price:     &price,
				Size:      &size,
			},
		},
	}

	client.processFeedData(1, data)

	feedData := client.channels[1].Symbols["SPY"]
	assert.Equal(t, "Trade", feedData.Trade.EventType)
	assert.Equal(t, "SPY", feedData.Trade.Symbol)
	assert.Equal(t, 450.0, *feedData.Trade.Price)
	assert.Equal(t, 100.0, *feedData.Trade.Size)
}

func TestProcessFeedData_Quotes(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(3, []string{"Quote"}, ".SPY240115C450")

	bid := 5.5
	ask := 5.6
	data := ProcessedFeedData{
		Quotes: []QuoteEvent{
			{
				EventType: "Quote",
				Symbol:    ".SPY240115C450",
				BidPrice:  &bid,
				AskPrice:  &ask,
			},
		},
	}

	client.processFeedData(3, data)

	feedData := client.channels[3].Symbols[".SPY240115C450"]
	assert.Equal(t, 5.5, *feedData.Quote.BidPrice)
	assert.Equal(t, 5.6, *feedData.Quote.AskPrice)
}

func TestProcessFeedData_Greeks(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(3, []string{"Greeks"}, ".SPY240115C450")

	delta := 0.5
	gamma := 0.02
	data := ProcessedFeedData{
		Greeks: []GreeksEvent{
			{
				EventType: "Greeks",
				Symbol:    ".SPY240115C450",
				Delta:     &delta,
				Gamma:     &gamma,
			},
		},
	}

	client.processFeedData(3, data)

	feedData := client.channels[3].Symbols[".SPY240115C450"]
	assert.Equal(t, 0.5, *feedData.Greek.Delta)
	assert.Equal(t, 0.02, *feedData.Greek.Gamma)
}

func TestProcessFeedData_WithCallback(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	callbackCalled := false
	var receivedData ProcessedFeedData

	client.WithDataCallback(1, func(data ProcessedFeedData) {
		callbackCalled = true
		receivedData = data
	})

	price := 450.0
	data := ProcessedFeedData{
		Trades: []TradeEvent{
			{
				EventType: "Trade",
				Symbol:    "SPY",
				Price:     &price,
			},
		},
	}

	client.processFeedData(1, data)

	assert.True(t, callbackCalled)
	assert.Len(t, receivedData.Trades, 1)
	assert.Equal(t, "SPY", receivedData.Trades[0].Symbol)
}

func TestFuturesEventProducer(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token").WithFutures("/ES")

	price := 4500.0
	data := ProcessedFeedData{
		Trades: []TradeEvent{
			{
				EventType: "Trade",
				Symbol:    "/ES",
				Price:     &price,
			},
		},
	}

	go client.processFeedData(5, data)

	select {
	case received := <-client.FuturesEventProducer():
		assert.Len(t, received.Trades, 1)
		assert.Equal(t, "/ES", received.Trades[0].Symbol)
		assert.Equal(t, 4500.0, *received.Trades[0].Price)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for futures data")
	}
}

func TestGetDefaultEventFields(t *testing.T) {
	tests := []struct {
		eventType string
		expected  []string
	}{
		{
			eventType: "Quote",
			expected:  []string{"eventType", "eventSymbol", "bidPrice", "askPrice"},
		},
		{
			eventType: "Trade",
			expected:  []string{"eventType", "eventSymbol", "price", "size"},
		},
		{
			eventType: "Greeks",
			expected:  []string{"eventType", "eventSymbol", "price", "volatility", "delta", "gamma", "theta", "rho", "vega"},
		},
		{
			eventType: "Candle",
			expected:  []string{"eventType", "eventSymbol", "time", "open", "high", "low", "close", "volume", "impVolatility", "openInterest"},
		},
		{
			eventType: "Unknown",
			expected:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			result := getDefaultEventFields(tt.eventType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertToFeedEventFields(t *testing.T) {
	eventFields := map[string][]string{
		"Quote":  {"eventType", "eventSymbol", "bidPrice", "askPrice"},
		"Trade":  {"eventType", "eventSymbol", "price"},
		"Greeks": {"eventType", "eventSymbol", "delta"},
		"Candle": {"eventType", "eventSymbol", "close"},
	}

	result := convertToFeedEventFields(eventFields)

	assert.Equal(t, eventFields["Quote"], result.Quote)
	assert.Equal(t, eventFields["Trade"], result.Trade)
	assert.Equal(t, eventFields["Greeks"], result.Greeks)
	assert.Equal(t, eventFields["Candle"], result.Candle)
}

func TestSendSubscriptionsInChunks(t *testing.T) {
	messagesSent := 0
	server := newMockWSServer(func(conn *websocket.Conn) {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msgMap map[string]any
			json.Unmarshal(msg, &msgMap)

			msgType, ok := msgMap["type"].(string)
			if !ok {
				continue
			}

			if msgType == "SETUP" {
				response := SetupMsg{
					Type:    "SETUP",
					Channel: 0,
					Version: "0.1-test",
				}
				conn.WriteJSON(response)
			} else if msgType == "FEED_SUBSCRIPTION" {
				messagesSent++
			}
		}
	})
	defer server.Close()

	ctx := context.Background()
	client := New(ctx, server.URL(), "test-token")

	err := client.Connect()
	require.NoError(t, err)
	defer client.Close()

	time.Sleep(50 * time.Millisecond)

	// Create 200 subscription items (more than chunk size of 90)
	items := make([]FeedSubItem, 200)
	for i := 0; i < 200; i++ {
		items[i] = FeedSubItem{
			Type:   "Trade",
			Symbol: "SYM" + string(rune(i)),
		}
	}

	client.sendSubscriptionsInChunks(1, items, 90)

	time.Sleep(100 * time.Millisecond)

	// Should send 3 messages: 90 + 90 + 20
	assert.Equal(t, 3, messagesSent)
}

func TestAddSubscription_AfterConnect(t *testing.T) {
	messagesSent := 0
	server := newMockWSServer(func(conn *websocket.Conn) {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msgMap map[string]any
			json.Unmarshal(msg, &msgMap)

			msgType := msgMap["type"].(string)

			if msgType == "SETUP" {
				response := SetupMsg{
					Type:    "SETUP",
					Channel: 0,
					Version: "0.1-test",
				}
				conn.WriteJSON(response)
			} else if msgType == "FEED_SUBSCRIPTION" {
				messagesSent++
			}
		}
	})
	defer server.Close()

	ctx := context.Background()
	client := New(ctx, server.URL(), "test-token")

	err := client.Connect()
	require.NoError(t, err)
	defer client.Close()

	time.Sleep(50 * time.Millisecond)

	err = client.AddSubscription(1, []string{"Trade"}, "AAPL", "MSFT")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	assert.Greater(t, messagesSent, 0)
}

func TestRemoveSubscription(t *testing.T) {
	messagesSent := 0
	var sentMessage FeedSubscriptionMsg

	server := newMockWSServer(func(conn *websocket.Conn) {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msgMap map[string]any
			json.Unmarshal(msg, &msgMap)

			msgType, ok := msgMap["type"].(string)
			if !ok {
				continue
			}

			if msgType == "SETUP" {
				response := SetupMsg{
					Type:    "SETUP",
					Channel: 0,
					Version: "0.1-test",
				}
				conn.WriteJSON(response)
			} else if msgType == "FEED_SUBSCRIPTION" {
				json.Unmarshal(msg, &sentMessage)
				messagesSent++
			}
		}
	})
	defer server.Close()

	ctx := context.Background()
	client := New(ctx, server.URL(), "test-token")

	client.AddSymbols(1, []string{"Trade"}, "SPY", "QQQ", "AAPL")
	assert.Len(t, client.channels[1].Symbols, 3)

	err := client.Connect()
	require.NoError(t, err)
	defer client.Close()

	time.Sleep(50 * time.Millisecond)

	err = client.RemoveSubscription(1, []string{"Trade"}, "SPY", "QQQ")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	assert.Len(t, client.channels[1].Symbols, 1)
	assert.Contains(t, client.channels[1].Symbols, "AAPL")
	assert.NotContains(t, client.channels[1].Symbols, "SPY")
	assert.NotContains(t, client.channels[1].Symbols, "QQQ")

	// Check message sent
	assert.Greater(t, messagesSent, 0)
	assert.Equal(t, FeedSubscription, sentMessage.Type)
	assert.Len(t, sentMessage.Remove, 2)
}

func TestChainedConfiguration(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token").
		WithLogger(slog.Default()).
		WithEquities("SPY", "QQQ").
		WithOptions(".SPY240115C450").
		WithFutures("/ES").
		WithDataCallback(1, func(data ProcessedFeedData) {})

	assert.NotNil(t, client.dxlog)
	assert.Len(t, client.channels, 3)
	assert.Len(t, client.channels[1].Symbols, 2) // Equities
	assert.Len(t, client.channels[3].Symbols, 1) // Options
	assert.Len(t, client.channels[5].Symbols, 1) // Futures
	assert.Len(t, client.dataCallbacks, 1)
	assert.NotNil(t, client.futuresEvent)
}
