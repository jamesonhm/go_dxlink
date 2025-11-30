package go_dxlink

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFuturesTradeFeedData(t *testing.T) {
	const futureSymbol = "/MESZ5"
	ctx := context.TODO()
	ctx, cancel := context.WithCancel(ctx)
	c := &DxLinkClient{
		ctx:        ctx,
		cancel:     cancel,
		retries:    3,
		delay:      1 * time.Second,
		expBackoff: false,
	}
	c.WithFuture(futureSymbol)
	assert.NotNil(t, c.futuresSubs)
	c.processMessage([]byte(tradeFeedData))
	price, err := c.GetFuturesPrice(futureSymbol)
	require.NoError(t, err)
	assert.Equal(t, 123.45, price)
}

func TestFuturesProducer(t *testing.T) {
	const futureSymbol = "/MESZ5"
	ctx := context.TODO()
	ctx, cancel := context.WithCancel(ctx)
	c := &DxLinkClient{
		ctx:        ctx,
		cancel:     cancel,
		retries:    3,
		delay:      1 * time.Second,
		expBackoff: false,
	}
	c.WithFuture(futureSymbol)
	assert.NotNil(t, c.futuresSubs)
	c.processMessage([]byte(tradeFeedSeq[0]))
	c.processMessage([]byte(tradeFeedSeq[1]))
	select {
	case pfd := <-c.FuturesEventProducer():
		fmt.Println("pfd:", pfd)
		assert.Equal(t, 124.45, pfd.Trades[0].Price)
	default:
	}
}

const tradeFeedData = `{
	"type": "FEED_DATA",
	"channel": 5,
	"data": [
		"Trade",
		[
			"Trade",
			"/MESZ5",
			123.45,
			456
		]
	]
}`

var tradeFeedSeq []string = []string{
	`{
	"type": "FEED_DATA",
	"channel": 5,
	"data": [
		"Trade",
		[
			"Trade",
			"/MESZ5",
			123.45,
			456
		]
	]
}`,
	`{
	"type": "FEED_DATA",
	"channel": 5,
	"data": [
		"Trade",
		[
			"Trade",
			"/MESZ5",
			124.45,
			456
		]
	]
}`,
	`{
	"type": "FEED_DATA",
	"channel": 5,
	"data": [
		"Trade",
		[
			"Trade",
			"/MESZ5",
			125.45,
			456
		]
	]
}`,
}
