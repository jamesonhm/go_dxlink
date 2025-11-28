package go_dxlink

import (
	"fmt"
	"log/slog"
	"math"
	"time"
)

func (c *DxLinkClient) OptionDataByOffset(
	underlying string,
	dte int,
	optType OptionType,
	offsetFrom float64,
	offsetBy int,
	holidays []time.Time,
) (*OptionData, error) {
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

// searches the map of optionSubs for the date, and strike nearest the delta based on the rounding value
func (c *DxLinkClient) OptionDataByDelta(
	underlying string,
	dte int,
	optType OptionType,
	round int,
	targetDelta float64,
	holidays []time.Time,
) (*OptionData, error) {
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
	atm, err := c.getUnderlyingPrice(underlying)
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

func (c *DxLinkClient) getUnderlyingData(sym string) (*UnderlyingData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	delay := c.delay
	for i := 0; i < c.retries; i++ {
		if underlyingPtr, ok := c.underlyingSubs[sym]; ok {
			if underlyingPtr != nil {
				return underlyingPtr, nil
			}
		}
		fmt.Printf("Retrying getUnderlyingData, attempt %d, delay: %s\n", i+1, delay.String())
		time.Sleep(delay)
		if c.expBackoff {
			delay *= 2
		}
	}
	return nil, fmt.Errorf("unable to find underlying in subscription data or is nil: %s", sym)
}
func (c *DxLinkClient) getUnderlyingPrice(sym string) (float64, error) {
	data, err := c.getUnderlyingData(sym)
	if err != nil {
		return 0.0, err
	}
	if data.Trade.Price == nil {
		return 0, fmt.Errorf("underlyingData.Trade.Price is a nil ptr")
	}
	price := *data.Trade.Price
	return price, nil

}

func (c *DxLinkClient) GetOptData(opt string) (*OptionData, error) {
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
		if optionDataPtr, ok := c.optionSubs[opt]; !ok {
			delay = retry(i, delay)
		} else if optionDataPtr == nil {
			delay = retry(i, delay)
		} else if optionDataPtr.Greek.Delta == nil ||
			optionDataPtr.Quote.AskPrice == nil ||
			*optionDataPtr.Quote.AskPrice == 0.0 ||
			optionDataPtr.Quote.BidPrice == nil ||
			*optionDataPtr.Quote.BidPrice == 0.0 {
			delay = retry(i, delay)
		} else {
			return optionDataPtr, nil
		}
	}
	return nil, fmt.Errorf("unable to find option in subscription data or is nil: %s", opt)
}

func (c *DxLinkClient) getOptDelta(opt string) (float64, error) {
	optionDataPtr, err := c.GetOptData(opt)
	if err != nil {
		return 0, err
	}
	if optionDataPtr.Greek.Delta == nil {
		return 0, fmt.Errorf("optionData.Greek.Delta is a nil ptr")
	}
	delta := *optionDataPtr.Greek.Delta
	return delta, nil
}

func roundNearest(price float64, round int) float64 {
	if round == 0 || round == 1 {
		return math.Floor(price)
	}
	diff := int(math.Floor(price)) % round
	return math.Floor(price) - float64(diff)
}
