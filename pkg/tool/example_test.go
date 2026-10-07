package tool_test

import (
	"context"
	"fmt"

	"github.com/sonnes/gopi/pkg/tool"
)

type cityInput struct {
	City string `json:"city" jsonschema:"the city to look up"`
}

func getWeather(_ context.Context, in cityInput) (string, error) {
	return "Sunny in " + in.City, nil
}

// A deferred tool waits until a tool change adds it, so it costs no
// tokens until the model needs it.
func ExampleNew() {
	weather := tool.New("get_weather", getWeather,
		tool.Description("Get the weather forecast."),
		tool.Deferred(),
	)

	info := weather.Info()
	fmt.Println(info.Name, info.Deferred)
	// Output: get_weather true
}

// Defer marks a tool that takes no option, such as a tool from another
// package.
func ExampleDefer() {
	stocks := tool.New("stock_price", getWeather)

	fmt.Println(tool.Defer(stocks).Info().Deferred)
	// Output: true
}
