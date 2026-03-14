package main

import (
	"fmt"
	"trade-ops-sentinel/internal/services/charts"
)

func main() {
	labels := []string{"03-01 04:16", "03-01 05:00", "03-01 06:00", "03-01 07:00", "03-01 08:00"}
	values := []float64{0.48, 1.27, 0.59, 2.34, 1.12}

	url := charts.BuildCumulativeProfitChartURL("Test Cumulative Profit", labels, values, "USDT", "dark", "standard", true, true)
	fmt.Println("Generated URL:")
	fmt.Println(url)
}
