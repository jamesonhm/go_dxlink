package go_dxlink

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetData(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	price := 450.0
	client.channels[1].Symbols["SPY"].Trade.Price = &price

	data, err := client.GetData("SPY")
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 450.0, *data.Trade.Price)
}

func TestGetData_NotFound(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	data, err := client.GetData("NONEXISTENT")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "symbol not found")
}

func TestGetData_MultipleChannels(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// Add symbols to different channels
	client.AddSymbols(1, []string{"Trade"}, "SPY")
	client.AddSymbols(3, []string{"Quote"}, ".SPY240115C450")
	client.AddSymbols(5, []string{"Trade"}, "/ES")

	// Should find in any channel
	data1, err := client.GetData("SPY")
	require.NoError(t, err)
	assert.NotNil(t, data1)

	data2, err := client.GetData(".SPY240115C450")
	require.NoError(t, err)
	assert.NotNil(t, data2)

	data3, err := client.GetData("/ES")
	require.NoError(t, err)
	assert.NotNil(t, data3)
}

func TestGetDataFromChannel(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")
	client.AddSymbols(3, []string{"Quote"}, "SPY")

	// Get from specific channel
	data, err := client.GetDataFromChannel(1, "SPY")
	require.NoError(t, err)
	assert.NotNil(t, data)
}

func TestGetDataFromChannel_WrongChannel(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")
	client.AddSymbols(3, []string{"Trade"}, "QQQ")

	// Try to get from wrong channel
	data, err := client.GetDataFromChannel(3, "SPY")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "symbol not found in channel")
}

func TestGetDataFromChannel_ChannelNotConfigured(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	data, err := client.GetDataFromChannel(99, "SPY")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "channel 99 not configured")
}

func TestGetUnderlyingData(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	price := 450.0
	client.channels[1].Symbols["SPY"].Trade.Price = &price

	data, err := client.GetUnderlyingData("SPY")
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 450.0, *data.Trade.Price)
}

func TestGetUnderlyingData_FromNonStandardChannel(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// Add to channel 7 instead of 1
	client.AddSymbols(7, []string{"Trade"}, "SPY")

	price := 450.0
	client.channels[7].Symbols["SPY"].Trade.Price = &price

	// Should still find it by searching all channels
	data, err := client.GetUnderlyingData("SPY")
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 450.0, *data.Trade.Price)
}

func TestGetUnderlyingData_NotFound(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// Reduce retries for faster test
	client.retries = 1
	client.delay = 10 * time.Millisecond

	data, err := client.GetUnderlyingData("NONEXISTENT")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unable to find underlying")
}

func TestGetUnderlyingPrice(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	price := 450.0
	client.channels[1].Symbols["SPY"].Trade.Price = &price

	actualPrice, err := client.GetUnderlyingPrice("SPY")
	require.NoError(t, err)
	assert.Equal(t, 450.0, actualPrice)
}

func TestGetUnderlyingPrice_NilPrice(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	// Price pointer is initialized but not set by AddSymbols
	// We need to explicitly set it to nil or leave it as the zero value
	// The issue is that Trade.Price is already a *float64 initialized by WithTrade()
	// So we need to verify it handles the case where price pointer is nil
	client.channels[1].Symbols["SPY"].Trade.Price = nil

	price, err := client.GetUnderlyingPrice("SPY")
	assert.Error(t, err)
	assert.Equal(t, 0.0, price)
	assert.Contains(t, err.Error(), "nil ptr")
}

func TestGetFuturesData(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(5, []string{"Trade"}, "/ES")

	price := 4500.0
	client.channels[5].Symbols["/ES"].Trade.Price = &price

	data, err := client.GetFuturesData("/ES")
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 4500.0, *data.Trade.Price)
}

func TestGetFuturesData_FromNonStandardChannel(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// Add to channel 7 instead of 5
	client.AddSymbols(7, []string{"Trade"}, "/ES")

	price := 4500.0
	client.channels[7].Symbols["/ES"].Trade.Price = &price

	// Should still find it
	data, err := client.GetFuturesData("/ES")
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 4500.0, *data.Trade.Price)
}

func TestGetFuturesPrice(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(5, []string{"Trade"}, "/ES")

	price := 4500.0
	client.channels[5].Symbols["/ES"].Trade.Price = &price

	actualPrice, err := client.GetFuturesPrice("/ES")
	require.NoError(t, err)
	assert.Equal(t, 4500.0, actualPrice)
}

func TestGetOptData(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	optSymbol := ".SPY240115C450"
	client.AddSymbols(3, []string{"Quote", "Greeks"}, optSymbol)

	// Set valid data
	bid := 5.5
	ask := 5.6
	delta := 0.5
	client.channels[3].Symbols[optSymbol].Quote.BidPrice = &bid
	client.channels[3].Symbols[optSymbol].Quote.AskPrice = &ask
	client.channels[3].Symbols[optSymbol].Greek.Delta = &delta

	data, err := client.GetOptData(optSymbol)
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 5.5, *data.Quote.BidPrice)
	assert.Equal(t, 5.6, *data.Quote.AskPrice)
	assert.Equal(t, 0.5, *data.Greek.Delta)
}

func TestGetOptData_MissingDelta(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")
	client.retries = 1
	client.delay = 10 * time.Millisecond

	optSymbol := ".SPY240115C450"
	client.AddSymbols(3, []string{"Quote", "Greeks"}, optSymbol)

	// Set quote but not delta
	bid := 5.5
	ask := 5.6
	client.channels[3].Symbols[optSymbol].Quote.BidPrice = &bid
	client.channels[3].Symbols[optSymbol].Quote.AskPrice = &ask
	// Delta is nil

	data, err := client.GetOptData(optSymbol)
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestGetOptData_ZeroBidAsk(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")
	client.retries = 1
	client.delay = 10 * time.Millisecond

	optSymbol := ".SPY240115C450"
	client.AddSymbols(3, []string{"Quote", "Greeks"}, optSymbol)

	// Set zero prices (invalid)
	bid := 0.0
	ask := 0.0
	delta := 0.5
	client.channels[3].Symbols[optSymbol].Quote.BidPrice = &bid
	client.channels[3].Symbols[optSymbol].Quote.AskPrice = &ask
	client.channels[3].Symbols[optSymbol].Greek.Delta = &delta

	data, err := client.GetOptData(optSymbol)
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestGetOptDelta(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	optSymbol := ".SPY240115C450"
	client.AddSymbols(3, []string{"Quote", "Greeks"}, optSymbol)

	// Set valid data
	bid := 5.5
	ask := 5.6
	delta := 0.5
	client.channels[3].Symbols[optSymbol].Quote.BidPrice = &bid
	client.channels[3].Symbols[optSymbol].Quote.AskPrice = &ask
	client.channels[3].Symbols[optSymbol].Greek.Delta = &delta

	actualDelta, err := client.getOptDelta(optSymbol)
	require.NoError(t, err)
	assert.Equal(t, 0.5, actualDelta)
}

func TestGetAllSymbols(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY", "QQQ")
	client.AddSymbols(3, []string{"Quote"}, ".SPY240115C450")
	client.AddSymbols(5, []string{"Trade"}, "/ES")

	allSymbols := client.GetAllSymbols()

	assert.Len(t, allSymbols, 4)
	assert.Contains(t, allSymbols, "SPY")
	assert.Contains(t, allSymbols, "QQQ")
	assert.Contains(t, allSymbols, ".SPY240115C450")
	assert.Contains(t, allSymbols, "/ES")
}

func TestGetAllSymbols_Empty(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	allSymbols := client.GetAllSymbols()

	assert.NotNil(t, allSymbols)
	assert.Len(t, allSymbols, 0)
}

func TestGetSymbolsByChannel(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY", "QQQ", "AAPL")
	client.AddSymbols(3, []string{"Quote"}, ".SPY240115C450")

	symbols, err := client.GetSymbolsByChannel(1)
	require.NoError(t, err)
	assert.Len(t, symbols, 3)
	assert.Contains(t, symbols, "SPY")
	assert.Contains(t, symbols, "QQQ")
	assert.Contains(t, symbols, "AAPL")
	assert.NotContains(t, symbols, ".SPY240115C450")
}

func TestGetSymbolsByChannel_NotConfigured(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	symbols, err := client.GetSymbolsByChannel(99)
	assert.Error(t, err)
	assert.Nil(t, symbols)
	assert.Contains(t, err.Error(), "channel 99 not configured")
}

func TestGetSymbolsByChannel_ReturnsImmutableCopy(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	symbols, err := client.GetSymbolsByChannel(1)
	require.NoError(t, err)

	// Modify the returned map
	delete(symbols, "SPY")

	// Original should still contain the symbol
	assert.Contains(t, client.channels[1].Symbols, "SPY")
}

func TestGetSymbolsByEventType(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY", "QQQ")
	client.AddSymbols(3, []string{"Quote", "Greeks"}, ".SPY240115C450")
	client.AddSymbols(5, []string{"Trade"}, "/ES")

	// Get all symbols with Trade event type
	tradeSymbols := client.GetSymbolsByEventType("Trade")
	assert.Len(t, tradeSymbols, 3)
	assert.Contains(t, tradeSymbols, "SPY")
	assert.Contains(t, tradeSymbols, "QQQ")
	assert.Contains(t, tradeSymbols, "/ES")
	assert.NotContains(t, tradeSymbols, ".SPY240115C450")

	// Get all symbols with Quote event type
	quoteSymbols := client.GetSymbolsByEventType("Quote")
	assert.Len(t, quoteSymbols, 1)
	assert.Contains(t, quoteSymbols, ".SPY240115C450")

	// Get all symbols with Greeks event type
	greekSymbols := client.GetSymbolsByEventType("Greeks")
	assert.Len(t, greekSymbols, 1)
	assert.Contains(t, greekSymbols, ".SPY240115C450")
}

func TestGetSymbolsByEventType_NoMatches(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	// Get symbols with event type that doesn't exist
	symbols := client.GetSymbolsByEventType("Candle")
	assert.NotNil(t, symbols)
	assert.Len(t, symbols, 0)
}

func TestOptionDataByOffset(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// Setup underlying
	client.AddSymbols(1, []string{"Trade"}, "SPY")
	underlyingPrice := 450.0
	client.channels[1].Symbols["SPY"].Trade.Price = &underlyingPrice

	// Setup option at strike 450
	exp := time.Now().AddDate(0, 0, 7)
	optSymbol := OptionSymbol{
		Underlying: "SPY",
		Date:       Midnight(exp),
		Strike:     450.0,
		OptionType: CallOption,
	}
	client.AddSymbols(3, []string{"Quote", "Greeks"}, optSymbol.DxLinkString())

	// Set valid option data
	bid := 5.5
	ask := 5.6
	delta := 0.5
	client.channels[3].Symbols[optSymbol.DxLinkString()].Quote.BidPrice = &bid
	client.channels[3].Symbols[optSymbol.DxLinkString()].Quote.AskPrice = &ask
	client.channels[3].Symbols[optSymbol.DxLinkString()].Greek.Delta = &delta

	data, err := client.OptionDataByOffset("SPY", 7, CallOption, 450.0, 0, []time.Time{})
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 0.5, *data.Greek.Delta)
}

func TestOptionDataByDelta(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// Setup underlying
	client.AddSymbols(1, []string{"Trade"}, "SPY")
	underlyingPrice := 450.0
	client.channels[1].Symbols["SPY"].Trade.Price = &underlyingPrice

	exp := time.Now().AddDate(0, 0, 7)

	// Setup multiple options at different strikes
	strikes := []float64{445.0, 450.0, 455.0}
	deltas := []float64{0.6, 0.5, 0.4}

	for i, strike := range strikes {
		optSymbol := OptionSymbol{
			Underlying: "SPY",
			Date:       Midnight(exp),
			Strike:     strike,
			OptionType: CallOption,
		}
		client.AddSymbols(3, []string{"Quote", "Greeks"}, optSymbol.DxLinkString())

		bid := 5.5
		ask := 5.6
		delta := deltas[i]
		client.channels[3].Symbols[optSymbol.DxLinkString()].Quote.BidPrice = &bid
		client.channels[3].Symbols[optSymbol.DxLinkString()].Quote.AskPrice = &ask
		client.channels[3].Symbols[optSymbol.DxLinkString()].Greek.Delta = &delta
	}

	// Search for delta closest to 0.5
	data, err := client.OptionDataByDelta("SPY", 7, CallOption, 5, 0.5, []time.Time{})
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 0.5, *data.Greek.Delta)
}

func TestOptionDataByDelta_NoUnderlyingPrice(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// No underlying price set
	data, err := client.OptionDataByDelta("SPY", 7, CallOption, 5, 0.5, []time.Time{})
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "unable to get underlying price")
}

func TestRoundNearest(t *testing.T) {
	tests := []struct {
		name     string
		price    float64
		round    int
		expected float64
	}{
		{
			name:     "Round to 1",
			price:    450.75,
			round:    1,
			expected: 450.0,
		},
		{
			name:     "Round to 5",
			price:    453.25,
			round:    5,
			expected: 450.0,
		},
		{
			name:     "Round to 5 - exact",
			price:    455.0,
			round:    5,
			expected: 455.0,
		},
		{
			name:     "Round to 10",
			price:    453.25,
			round:    10,
			expected: 450.0,
		},
		{
			name:     "Round to 10 - higher",
			price:    458.25,
			round:    10,
			expected: 450.0,
		},
		{
			name:     "Round zero",
			price:    453.25,
			round:    0,
			expected: 453.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := roundNearest(tt.price, tt.round)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDataRaceConditions(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	client.AddSymbols(1, []string{"Trade"}, "SPY")

	price := 450.0
	client.channels[1].Symbols["SPY"].Trade.Price = &price

	// Run concurrent reads
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_, _ = client.GetUnderlyingPrice("SPY")
			_ = client.GetAllSymbols()
			_, _ = client.GetSymbolsByChannel(1)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestMultipleChannelsSameSymbol(t *testing.T) {
	ctx := context.Background()
	client := New(ctx, "ws://test", "token")

	// Add same symbol to different channels with different event types
	client.AddSymbols(1, []string{"Trade"}, "SPY")
	client.AddSymbols(7, []string{"Quote"}, "SPY")

	tradePrice := 450.0
	bid := 449.5
	ask := 450.5

	client.channels[1].Symbols["SPY"].Trade.Price = &tradePrice
	client.channels[7].Symbols["SPY"].Quote.BidPrice = &bid
	client.channels[7].Symbols["SPY"].Quote.AskPrice = &ask

	// GetData should find first occurrence
	data, err := client.GetData("SPY")
	require.NoError(t, err)
	assert.NotNil(t, data)

	// Get from specific channels
	data1, err := client.GetDataFromChannel(1, "SPY")
	require.NoError(t, err)
	assert.Equal(t, 450.0, *data1.Trade.Price)

	data7, err := client.GetDataFromChannel(7, "SPY")
	require.NoError(t, err)
	assert.Equal(t, 449.5, *data7.Quote.BidPrice)
}
