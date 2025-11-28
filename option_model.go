package go_dxlink

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type OptionType string

const (
	// Option type - Call or Put
	PutOption  OptionType = "P"
	CallOption OptionType = "C"
)

type OptionSymbol struct {
	Underlying string
	Date       time.Time
	OptionType OptionType
	Strike     float64
}

// build option symbol in correct OCC Symbology
// Root symbol of the underlying stock or ETF, padded with spaces to 6 characters.
// Expiration date, 6 digits in the format yymmdd. Option type, either P or C, for
// put or call.
func (o OptionSymbol) OCCString() string {
	expiryString := o.Date.Format("060102")
	strikeString := getStrikeWithPadding(o.Strike)
	symbol := getSymbolWithPadding(o.Underlying)
	return fmt.Sprintf("%s%s%s%s", symbol, expiryString, o.OptionType, strikeString)
}

// convert the strike into a string with correct padding.
func getStrikeWithPadding(strike float64) string {
	strikeString := fmt.Sprintf("%d", int(strike*1000))
	for len(strikeString) < 8 {
		strikeString = "0" + strikeString
	}
	return strikeString
}

// convert the symbol into a string with correct padding.
func getSymbolWithPadding(symbol string) string {
	for len(symbol) < 6 {
		symbol += " "
	}

	return symbol
}

func (o OptionSymbol) DxLinkString() string {
	return fmt.Sprintf(".%s%s%s%.0f", o.Underlying, o.Date.Format("060102"), o.OptionType, o.Strike)
}

func ParseDxLinkOption(option string) (*OptionSymbol, error) {
	var split int
	if string(option[0]) != "." {
		return nil, fmt.Errorf("unrecognized option format, no '.' prefix: %s", option)
	}
	for i, r := range option {
		if string(r) == "." || unicode.IsLetter(r) {
			continue
		}
		if unicode.IsDigit(r) {
			split = i
			break
		}
		return nil, fmt.Errorf("unrecognized option format, no number found: %s", option)
	}
	opt_date := option[split : split+6]
	date, err := time.Parse("060102", opt_date)
	if err != nil {
		return nil, fmt.Errorf("unable to parse date: %s, err: %w", opt_date, err)
	}
	opt_type := OptionType(option[split+6 : split+7])
	strike, err := strconv.ParseFloat(option[split+7:], 64)
	if err != nil {
		return nil, fmt.Errorf("unable to parse strike as float: %s, err: %w", option[split+7:], err)
	}

	res := OptionSymbol{
		Underlying: option[1:split],
		Date:       date,
		OptionType: opt_type,
		Strike:     strike,
	}
	return &res, nil
}

func ParseOCCOption(option string) (*OptionSymbol, error) {
	fields := strings.Fields(option)
	if len(fields) != 2 {
		return nil, fmt.Errorf("unrecognized option format, len(fields) != 2: %s", option)
	}
	symbol := fields[0]
	opt_date := fields[1][0:6]
	date, err := time.Parse("060102", opt_date)
	if err != nil {
		return nil, fmt.Errorf("unable to parse date: %s, err: %w", opt_date, err)
	}
	opt_types := fields[1][6:7]
	if opt_types != "P" && opt_types != "C" {
		return nil, fmt.Errorf("unable to parse type: %s, should be `P` or `C`", opt_types)
	}
	opt_type := OptionType(opt_types)
	strike_int, err := strconv.ParseInt(fields[1][7:], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unable to parse strike as int: %s, err: %w", fields[1][7:], err)
	}
	strike := float64(strike_int) / 1000

	res := OptionSymbol{
		Underlying: symbol,
		Date:       date,
		OptionType: opt_type,
		Strike:     strike,
	}
	return &res, nil
}

// returns a new optionSymbol with the strike offset by the amount arg
func (o OptionSymbol) NewRelative(amt float64) *OptionSymbol {
	return &OptionSymbol{
		Underlying: o.Underlying,
		Date:       o.Date,
		OptionType: o.OptionType,
		Strike:     o.Strike + amt,
	}
}

func (o *OptionSymbol) IncrementStrike(amt float64) {
	o.Strike += amt
}

func (o *OptionSymbol) DecrementStrike(amt float64) {
	o.Strike -= amt
}
