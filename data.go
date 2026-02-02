package go_dxlink

import (
	"fmt"
	"log/slog"
	"math"
	"time"
)

// GetData retrieves feedData for a symbol from any channel
// This is useful when you don't know which channel a symbol is on
func (c *DxLinkClient) GetData(symbol string) (*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, channelCfg := range c.channels {
		if data, ok := channelCfg.Symbols[symbol]; ok {
			return data, nil
		}
	}

	return nil, fmt.Errorf("symbol not found in any channel: %s", symbol)
}

// GetDataFromChannel retrieeves feedData for a symbol from a specific channel
func (c *DxLinkClient) GetDataFromChannel(channel int, symbol string) (*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	channelCfg, exists := c.channels[channel]
	if !exists {
		return nil, fmt.Errorf("channel %d not configured", channel)
	}

	data, ok := channelCfg.Symbols[symbol]
	if !ok {
		return nil, fmt.Errorf("symbol not found in channel %d: %s", channel, symbol)
	}

	return data, nil
}

// GetUnderlyingData retrieves feed data for an underlyingsymbol (typically from channel 1)
func (c *DxLinkClient) GetUnderlyingData(sym string) (*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	delay := c.delay
	for i := 0; i < c.retries; i++ {
		if channelCfg, ok := c.channels[1]; ok {
			if underlyingPtr, ok := channelCfg.Symbols[sym]; ok {
				if underlyingPtr != nil {
					return underlyingPtr, nil
				}
			}
		}

		// If not in channel 1, search all channels
		for _, channelCfg := range c.channels {
			if underlyingPtr, ok := channelCfg.Symbols[sym]; ok {
				if underlyingPtr != nil {
					return underlyingPtr, nil
				}
			}
		}

		c.mu.RUnlock()
		fmt.Printf("Retrying getUnderlyingData, attempt %d, delay: %s\n", i+1, delay.String())
		time.Sleep(delay)
		c.mu.RLock()

		if c.expBackoff {
			delay *= 2
		}
	}
	return nil, fmt.Errorf("unable to find underlying in subscription data or is nil: %s", sym)
}

// GetUnderlyingPrice retrieves the current price for an underlying symbol
func (c *DxLinkClient) GetUnderlyingPrice(sym string) (float64, error) {
	data, err := c.GetUnderlyingData(sym)
	if err != nil {
		return 0.0, err
	}
	if data.Trade.Price == nil {
		return 0.0, fmt.Errorf("underlyingData.Trade.Price is a nil ptr")
	}
	price := *data.Trade.Price
	return price, nil
}

// GetFuturesData retrieves  feed data for a futures symbol (typically from channel 5)
func (c *DxLinkClient) GetFuturesData(sym string) (*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	delay := c.delay
	for i := 0; i < c.retries; i++ {
		// Try channel 5 first
		if channelCfg, ok := c.channels[5]; ok {
			if futuresPtr, ok := channelCfg.Symbols[sym]; ok {
				if futuresPtr != nil {
					return futuresPtr, nil
				}
			}
		}

		// If not in channel 5, search all
		for _, channelCfg := range c.channels {
			if futuresPtr, ok := channelCfg.Symbols[sym]; ok {
				if futuresPtr != nil {
					return futuresPtr, nil
				}
			}
		}

		c.mu.RUnlock()
		fmt.Printf("Retrying getFuturesData, attempt %d, delay: %s\n", i+1, delay.String())
		time.Sleep(delay)
		c.mu.RLock()

		if c.expBackoff {
			delay *= 2
		}
	}
	return nil, fmt.Errorf("unable to find futures in subscription data or is nil: %s", sym)
}

// GetFuturesPrice retrieves the current price for the futures symbol
func (c *DxLinkClient) GetFuturesPrice(sym string) (float64, error) {
	data, err := c.GetFuturesData(sym)
	if err != nil {
		return 0.0, err
	}
	if data.Trade.Price == nil {
		return 0.0, fmt.Errorf("futuresData.Trade.Price is a nil ptr")
	}
	price := *data.Trade.Price
	return price, nil
}

// GetOptData retrieves option data for an option symbol (typically from channel 3)
func (c *DxLinkClient) GetOptData(opt string) (*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	delay := c.delay
	retry := func(i int, delay time.Duration) time.Duration {
		fmt.Printf("Retrying getOptData, attempt %d, delay: %s\n", i+1, delay.String())
		time.Sleep(delay)
		if c.expBackoff {
			delay *= 2
		}
		return delay
	}

	for i := 0; i < c.retries; i++ {
		if channelCfg, ok := c.channels[3]; ok {
			if optionDataPtr, ok := channelCfg.Symbols[opt]; ok {
				if optionDataPtr == nil {
					c.mu.RUnlock()
					delay = retry(i, delay)
					c.mu.RLock()
					continue
				}

				return optionDataPtr, nil
			}
		}

		// if not in channel 3, search all channels
		for _, channelCfg := range c.channels {
			if optionsDataPtr, ok := channelCfg.Symbols[opt]; ok {
				if optionsDataPtr == nil {
					continue
				}

				return optionsDataPtr, nil
			}
		}

		c.mu.RUnlock()
		delay = retry(i, delay)
		c.mu.RLock()
	}

	return nil, fmt.Errorf("unable to find option in subscription data or is nil: %s", opt)
}

// getOptDelta retrieves the delta for an option symbol
func (c *DxLinkClient) getOptDelta(opt string) (float64, error) {
	optionDataPtr, err := c.GetOptData(opt)
	if err != nil {
		return 0.0, err
	}
	if optionDataPtr.Greek.Delta == nil {
		return 0.0, fmt.Errorf("optionData.Greek.Delta is a nil ptr")
	}
	delta := *optionDataPtr.Greek.Delta
	return delta, nil
}

// OptionDataByOffset retrieves option feed data by strike offset from a reference price
func (c *DxLinkClient) OptionDataByOffset(
	underlying string,
	dte int,
	optType OptionType,
	offsetFrom float64,
	offsetBy int,
	holidays []time.Time,
) (*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	exp := DTEToDateHolidays(time.Now(), dte, holidays)
	s := float64(int(offsetFrom) + offsetBy)
	opt := OptionSymbol{
		Underlying: underlying,
		Date:       exp,
		Strike:     s,
		OptionType: optType,
	}
	data, err := c.GetOptData(opt.DxLinkString())
	if err != nil {
		return nil, fmt.Errorf("OptionDataByOffset: unable to find INITIAL option in subscription data: %s, %w", opt.DxLinkString(), err)
	}
	return data, nil
}

// OptionDataByDelta searches for the option with the delta nearest the targetDelta based on the rounding value
func (c *DxLinkClient) OptionDataByDelta(
	underlying string,
	dte int,
	optType OptionType,
	round int,
	targetDelta float64,
	holidays []time.Time,
) (*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var delta float64 = 0.0
	var dist float64 = 99.9
	var tempDist float64 = 0.0
	var tempOpt *OptionSymbol
	var err error
	var attempt int = 0

	// find exp date
	exp := DTEToDateHolidays(time.Now(), dte, holidays)
	atm, err := c.GetUnderlyingPrice(underlying)
	if err != nil {
		return nil, fmt.Errorf("OptionDataByDelta: unable to get underlying price for '%s'", underlying)
	}
	s := roundNearest(atm, round)
	opt := &OptionSymbol{
		Underlying: underlying,
		Date:       exp,
		Strike:     s,
		OptionType: optType,
	}

	for attempt < 5 {
		// condition initial starting point not found yet
		if delta == 0.0 && tempDist == 0.0 {
			delta, err = c.getOptDelta(opt.DxLinkString())
			if err != nil {
				attempt += 1
				tempOpt = opt.NewRelative(float64(round))
				slog.Info("unable to find initial delta", "option", opt.DxLinkString(), "attempt", attempt, "error", err)
				continue
			} else {
				attempt = 0
				tempDist = math.Abs(delta - targetDelta)
				slog.Info("found initial delta", "option", opt.DxLinkString(), "delta", delta, "dist", tempDist)
			}
		}
		// have a delta and a dist and a targetDelta
		if tempDist > dist {
			// return prev, non-temp opt
			optDataPtr, err := c.GetOptData(opt.DxLinkString())
			if err != nil {
				slog.Error("unable to get option data", "option", opt.DxLinkString(), "error", err)
				return nil, err
			}
			slog.Info("returning option data", "option", opt.DxLinkString())
			return optDataPtr, nil
		}
		if tempOpt != nil {
			opt = tempOpt
		}
		dist = tempDist
		if delta > targetDelta {
			// decrement new temp opt
			tempOpt = opt.NewRelative(-1 * float64(round))
			delta, err = c.getOptDelta(tempOpt.DxLinkString())
			if err != nil {
				attempt += 1
				slog.Info("unable to find delta", "option", tempOpt.DxLinkString(), "attempt", attempt, "error", err)
				continue
			}
			tempDist = math.Abs(delta - targetDelta)
			slog.Info("decremented option", "option", tempOpt.DxLinkString(), "delta", delta, "dist", tempDist)
		} else if delta < targetDelta {
			// increment new temp opt
			tempOpt = opt.NewRelative(float64(round))
			delta, err = c.getOptDelta(tempOpt.DxLinkString())
			if err != nil {
				attempt += 1
				slog.Info("unable to find delta", "option", tempOpt.DxLinkString(), "attempt", attempt, "error", err)
				continue
			}
			tempDist = math.Abs(delta - targetDelta)
			slog.Info("incremented option", "option", tempOpt.DxLinkString(), "delta", delta, "dist", tempDist)
		}
	}
	return nil, fmt.Errorf("OptionDataByDelta: Max attempts reached\n")
}

// ----- Discovery Methods -----

// GetAllSymbols returns all symbols across all channels
func (c *DxLinkClient) GetAllSymbols() map[string]*feedData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	allSymbols := make(map[string]*feedData)
	for _, channelCfg := range c.channels {
		for symbol, data := range channelCfg.Symbols {
			allSymbols[symbol] = data
		}
	}
	return allSymbols
}

// GetSymbolsByChannel returns all symbols for a specific channel
func (c *DxLinkClient) GetSymbolsByChannel(channel int) (map[string]*feedData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	channelCfg, exists := c.channels[channel]
	if !exists {
		return nil, fmt.Errorf("channel %d not configured", channel)
	}

	// Return a copy to prefvent external modification
	symbols := make(map[string]*feedData)
	for symbol, data := range channelCfg.Symbols {
		symbols[symbol] = data
	}
	return symbols, nil
}

// GetSymbolsByEventType returns all symbols that have a specific event type configured
func (c *DxLinkClient) GetSymbolsByEventType(eventType string) map[string]*feedData {
	c.mu.RLock()
	defer c.mu.RUnlock()

	symbols := make(map[string]*feedData)
	for _, channelCfg := range c.channels {
		// Check if this channel has the event type configured
		if _, hasEventType := channelCfg.EventFields[eventType]; hasEventType {
			for symbol, data := range channelCfg.Symbols {
				symbols[symbol] = data
			}
		}
	}
	return symbols
}

func roundNearest(price float64, round int) float64 {
	if round == 0 || round == 1 {
		return math.Floor(price)
	}
	diff := int(math.Floor(price)) % round
	return math.Floor(price) - float64(diff)
}
