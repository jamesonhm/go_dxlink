# go_dxlink

A Go client library for streaming real-time market data via WebSocket using the dxLink protocol. This library provides a clean, flexible API for subscribing to equities, options, and futures market data.

## Features

- 🔌 WebSocket-based real-time market data streaming
- 📊 Support for multiple asset types (equities, options, futures)
- 🎯 Flexible subscription management with channel-based organization
- 🔄 Automatic reconnection with exponential backoff
- 🧵 Thread-safe concurrent data access
- 📡 Multiple data delivery patterns (direct access, callbacks, channels)
- 🏗️ Fluent builder API for easy configuration

## Installation

```bash
go get github.com/jamesonhm/go_dxlink
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    
    dxlink "github.com/jamesonhm/go_dxlink"
)

func main() {
    ctx := context.Background()
    
    client := dxlink.New(ctx, "wss://demo.dxfeed.com/dxlink-ws", "your-token").
        WithEquities("SPY", "QQQ", "AAPL").
        WithOptions(".SPY240115C450", ".SPY240115P450").
        WithFutures("/ES", "/NQ")
    
    if err := client.Connect(); err != nil {
        log.Fatal(err)
    }
    defer client.Close()
    
    // Wait for data and retrieve it
    time.Sleep(2 * time.Second)
    
    price, err := client.GetUnderlyingPrice("SPY")
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("SPY Price: %.2f\n", price)
}
```

## Core Concepts

### Channels

The dxLink protocol uses **channels** to organize subscriptions. Each channel can have:
- Configured event types (Trade, Quote, Greeks, Candle)
- Custom field selections for each event type
- Multiple symbol subscriptions

By default:
- **Channel 1**: Equities (Trade events)
- **Channel 3**: Options (Quote and Greeks events)
- **Channel 5**: Futures (Trade events)

You can create custom channels with different configurations as needed.

### Event Types

- **Trade**: Last trade price, size, volume
- **Quote**: Best bid/ask prices and sizes
- **Greeks**: Option Greeks (delta, gamma, theta, vega, rho) and implied volatility
- **Candle**: OHLC candlestick data

## Constructor

```go
func New(ctx context.Context, url string, token string) *DxLinkClient
```

Creates a new dxLink client.

**Parameters:**
- `ctx`: Context for cancellation and lifecycle management
- `url`: WebSocket URL (e.g., "wss://demo.dxfeed.com/dxlink-ws")
- `token`: Authentication token

**Returns:** A new `DxLinkClient` instance ready for configuration

## Builder Methods

### WithLogger

```go
func (c *DxLinkClient) WithLogger(logger *slog.Logger) *DxLinkClient
```

Configures structured logging for debugging connection and message flow.

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
client := dxlink.New(ctx, url, token).WithLogger(logger)
```

### WithChannel

```go
func (c *DxLinkClient) WithChannel(config ChannelConfig) *DxLinkClient
```

Explicitly configure a channel with full control over all settings.

```go
client.WithChannel(dxlink.ChannelConfig{
    Channel:           3,
    AggregationPeriod: 30,
    EventFields: map[string][]string{
        "Quote": {"eventType", "eventSymbol", "bidPrice", "askPrice", "bidSize", "askSize"},
        "Greeks": {"eventType", "eventSymbol", "delta", "gamma", "theta", "vega"},
    },
})
```

### AddSymbols

```go
func (c *DxLinkClient) AddSymbols(channel int, eventTypes []string, symbols ...string) *DxLinkClient
```

Add symbols to a channel with specified event types. Auto-creates channel with defaults if it doesn't exist.

```go
client.AddSymbols(1, []string{"Trade", "Quote"}, "AAPL", "MSFT", "GOOGL")
client.AddSymbols(3, []string{"Quote", "Greeks"}, ".SPY240115C450")
```

### WithEquities

```go
func (c *DxLinkClient) WithEquities(symbols ...string) *DxLinkClient
```

Convenience method to subscribe to equity Trade events on channel 1.

```go
client.WithEquities("SPY", "QQQ", "AAPL", "MSFT")
```

### WithOptions

```go
func (c *DxLinkClient) WithOptions(symbols ...string) *DxLinkClient
```

Convenience method to subscribe to option Quote and Greeks events on channel 3.

```go
client.WithOptions(".SPY240115C450", ".SPY240115P450", ".QQQ240115C400")
```

### WithFutures

```go
func (c *DxLinkClient) WithFutures(symbols ...string) *DxLinkClient
```

Convenience method to subscribe to futures Trade events on channel 5. Also initializes the futures event channel.

```go
client.WithFutures("/ES", "/NQ", "/YM")
```

### WithDataCallback

```go
func (c *DxLinkClient) WithDataCallback(channel int, callback func(ProcessedFeedData)) *DxLinkClient
```

Register a callback function to be invoked when data arrives on a specific channel.

```go
client.WithDataCallback(3, func(data dxlink.ProcessedFeedData) {
    for _, greek := range data.Greeks {
        fmt.Printf("Symbol: %s, Delta: %.4f\n", greek.Symbol, *greek.Delta)
    }
})
```

## Connection Management

### Connect

```go
func (c *DxLinkClient) Connect() error
```

Establishes the WebSocket connection and initiates the authentication handshake.

```go
if err := client.Connect(); err != nil {
    log.Fatal(err)
}
```

### Close

```go
func (c *DxLinkClient) Close() error
```

Gracefully closes the WebSocket connection and cleans up resources.

```go
defer client.Close()
```

### AddSubscription

```go
func (c *DxLinkClient) AddSubscription(channel int, eventTypes []string, symbols ...string) error
```

Add subscriptions after connection is established.

```go
err := client.AddSubscription(1, []string{"Trade"}, "TSLA", "NVDA")
```

### RemoveSubscription

```go
func (c *DxLinkClient) RemoveSubscription(channel int, eventTypes []string, symbols ...string) error
```

Remove subscriptions from a channel.

```go
err := client.RemoveSubscription(1, []string{"Trade"}, "TSLA")
```

## Data Access Patterns

### Pattern 1: Direct Data Access (Polling)

Use the `data.go` methods to retrieve data on-demand. Best for periodic checks or one-time queries.

```go
// Get underlying equity price
price, err := client.GetUnderlyingPrice("SPY")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("SPY: $%.2f\n", price)

// Get full option data
optData, err := client.GetOptData(".SPY240115C450")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Bid: $%.2f, Ask: $%.2f, Delta: %.4f\n", 
    *optData.Quote.BidPrice, 
    *optData.Quote.AskPrice,
    *optData.Greek.Delta)

// Get futures price
futPrice, err := client.GetFuturesPrice("/ES")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("ES: $%.2f\n", futPrice)

// Get data from specific channel
data, err := client.GetDataFromChannel(3, ".SPY240115C450")

// Get all symbols for a channel
symbols, err := client.GetSymbolsByChannel(1)
for symbol, data := range symbols {
    if data.Trade.Price != nil {
        fmt.Printf("%s: $%.2f\n", symbol, *data.Trade.Price)
    }
}
```

### Pattern 2: Callback Functions (Push)

Register callbacks to process data as it arrives. Best for real-time processing and event-driven applications.

```go
// Callback for option data on channel 3
client.WithDataCallback(3, func(data dxlink.ProcessedFeedData) {
    // Process quotes
    for _, quote := range data.Quotes {
        spread := *quote.AskPrice - *quote.BidPrice
        fmt.Printf("[QUOTE] %s - Bid: %.2f, Ask: %.2f, Spread: %.2f\n",
            quote.Symbol, *quote.BidPrice, *quote.AskPrice, spread)
    }
    
    // Process greeks
    for _, greek := range data.Greeks {
        fmt.Printf("[GREEKS] %s - Delta: %.4f, Gamma: %.4f, Theta: %.4f\n",
            greek.Symbol, *greek.Delta, *greek.Gamma, *greek.Theta)
    }
})

// Callback for equity data on channel 1
client.WithDataCallback(1, func(data dxlink.ProcessedFeedData) {
    for _, trade := range data.Trades {
        fmt.Printf("[TRADE] %s - Price: %.2f, Size: %.0f\n",
            trade.Symbol, *trade.Price, *trade.Size)
    }
})

client.Connect()

// Callbacks will be invoked automatically as data arrives
select {} // Keep running
```

### Pattern 3: Go Channel (Futures Only)

Use Go channels for futures data. Best for concurrent processing pipelines.

```go
client.WithFutures("/ES", "/NQ", "/YM")

if err := client.Connect(); err != nil {
    log.Fatal(err)
}

// Get the futures event channel
futuresChan := client.FuturesEventProducer()

// Process futures data from channel
go func() {
    for data := range futuresChan {
        for _, trade := range data.Trades {
            fmt.Printf("Futures Update - %s: $%.2f (Size: %.0f)\n",
                trade.Symbol, *trade.Price, *trade.Size)
            
            // Send to another goroutine, database, etc.
            processTradeData(trade)
        }
    }
}()

select {} // Keep running
```

## Advanced Usage

### Option Selection by Delta

Find options closest to a target delta:

```go
// Find 30-delta call option
optData, err := client.OptionDataByDelta(
    "SPY",              // underlying
    30,                 // days to expiration
    dxlink.CallOption,  // option type
    5,                  // strike rounding (nearest $5)
    0.30,               // target delta
    []time.Time{},      // holidays to exclude
)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("30-delta call: Strike %.0f, Delta: %.4f\n",
    optData.Greek.Price, *optData.Greek.Delta)
```

### Option Selection by Strike Offset

Get option data at a specific strike offset:

```go
// Get call option $10 above current price
optData, err := client.OptionDataByOffset(
    "SPY",              // underlying
    30,                 // days to expiration
    dxlink.CallOption,  // option type
    450.0,              // reference price
    10,                 // offset (+10 = 460 strike)
    []time.Time{},      // holidays
)
```

### Custom Channel Configuration

```go
// Create a custom channel for candle data
client.WithChannel(dxlink.ChannelConfig{
    Channel:           7,
    AggregationPeriod: 60,
    EventFields: map[string][]string{
        "Candle": {
            "eventType", "eventSymbol", "time",
            "open", "high", "low", "close", "volume",
        },
    },
})

// Add candle subscriptions
client.AddSymbols(7, []string{"Candle"}, "SPY{=d}", "QQQ{=d}")

// Set up callback for candle data
client.WithDataCallback(7, func(data dxlink.ProcessedFeedData) {
    for _, candle := range data.Candles {
        fmt.Printf("Candle %s: O=%.2f H=%.2f L=%.2f C=%.2f V=%.0f\n",
            candle.Symbol, *candle.Open, *candle.High, 
            *candle.Low, *candle.Close, *candle.Volume)
    }
})
```

### Query and Discovery

```go
// Get all subscribed symbols
allSymbols := client.GetAllSymbols()
fmt.Printf("Total symbols: %d\n", len(allSymbols))

// Get symbols by event type
tradeSymbols := client.GetSymbolsByEventType("Trade")
for symbol := range tradeSymbols {
    fmt.Printf("Trading: %s\n", symbol)
}

// Check subscription count
count := client.LenSubscriptions(1)
fmt.Printf("Channel 1 has %d symbols\n", count)
```

## Complete Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "log/slog"
    "os"
    "os/signal"
    "time"
    
    dxlink "github.com/jamesonhm/go_dxlink"
)

func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    // Setup logger
    logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    }))
    
    // Create client with subscriptions
    client := dxlink.New(ctx, "wss://demo.dxfeed.com/dxlink-ws", "demo").
        WithLogger(logger).
        WithEquities("SPY", "QQQ").
        WithOptions(".SPY240315C450", ".SPY240315P450").
        WithFutures("/ES").
        WithDataCallback(1, handleEquityData).
        WithDataCallback(3, handleOptionData)
    
    // Connect
    if err := client.Connect(); err != nil {
        log.Fatal(err)
    }
    defer client.Close()
    
    // Process futures data in goroutine
    go processFuturesData(client.FuturesEventProducer())
    
    // Wait for initial data
    time.Sleep(3 * time.Second)
    
    // Query data periodically
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()
    
    // Handle shutdown
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt)
    
    for {
        select {
        case <-ticker.C:
            queryData(client)
        case <-sigChan:
            fmt.Println("\nShutting down...")
            return
        }
    }
}

func handleEquityData(data dxlink.ProcessedFeedData) {
    for _, trade := range data.Trades {
        fmt.Printf("[EQUITY] %s: $%.2f\n", trade.Symbol, *trade.Price)
    }
}

func handleOptionData(data dxlink.ProcessedFeedData) {
    for _, greek := range data.Greeks {
        fmt.Printf("[OPTION] %s: Delta=%.4f, IV=%.2f%%\n",
            greek.Symbol, *greek.Delta, *greek.Volatility*100)
    }
}

func processFuturesData(futuresChan <-chan dxlink.ProcessedFeedData) {
    for data := range futuresChan {
        for _, trade := range data.Trades {
            fmt.Printf("[FUTURES] %s: $%.2f\n", trade.Symbol, *trade.Price)
        }
    }
}

func queryData(client *dxlink.DxLinkClient) {
    price, err := client.GetUnderlyingPrice("SPY")
    if err == nil {
        fmt.Printf("\n=== Data Query ===\n")
        fmt.Printf("SPY Price: $%.2f\n", price)
        
        // Get all symbols
        symbols := client.GetAllSymbols()
        fmt.Printf("Total symbols tracked: %d\n", len(symbols))
    }
}
```

## Error Handling

The library uses standard Go error handling. Common errors:

```go
// Connection errors
if err := client.Connect(); err != nil {
    // Handle: invalid URL, authentication failure, network error
}

// Data not available yet
price, err := client.GetUnderlyingPrice("SPY")
if err != nil {
    // Handle: symbol not found, data not received yet, nil price
}

// Subscription errors
err := client.AddSubscription(1, []string{"Trade"}, "INVALID")
if err != nil {
    // Handle: not connected, invalid channel
}
```

## Thread Safety

All public methods are thread-safe and can be called concurrently from multiple goroutines.

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## Support

For issues and questions, please open an issue on GitHub.
