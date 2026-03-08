package charts

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
)

func chartDimensions(size string) (int, int) {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "compact":
		return 800, 420
	case "wide":
		return 1280, 600
	default:
		return 1000, 500
	}
}

func BuildLineChartURL(title string, labels []string, values []float64, unit, theme, size string, showLabels, showGrid bool) string {
	if len(labels) == 0 {
		return ""
	}
	dark := strings.EqualFold(strings.TrimSpace(theme), "dark")
	isPnL := strings.Contains(strings.ToUpper(title), "PNL")
	chartType := "line"
	lineColor := "#14b8a6"
	fillColor := "rgba(20,184,166,0.18)"
	if isPnL {
		lineColor = "#0ea5e9"
		fillColor = "rgba(14,165,233,0.18)"
	}

	datasets := make([]map[string]any, 0, 2)
	dataset := map[string]any{
		"label":           unit,
		"data":            values,
		"borderColor":     lineColor,
		"backgroundColor": fillColor,
		"fill":            true,
		"tension":         0.25,
		"pointRadius":     0,
		"borderWidth":     2,
	}
	if isPnL {
		dataset["borderColor"] = "#0ea5e9"
		dataset["backgroundColor"] = "rgba(14,165,233,0.18)"
	}
	datasets = append(datasets, dataset)

	bgColor := "white"
	titleColor := "#111827"
	legendColor := "#111827"
	tickColor := "#374151"
	gridColor := "rgba(0,0,0,0.06)"
	if dark {
		bgColor = "%23000000"
		titleColor = "#ffffff"
		legendColor = "#e5e7eb"
		tickColor = "#d1d5db"
		gridColor = "rgba(255,255,255,0.24)"
	}
	width, height := chartDimensions(size)

	xGrid := map[string]any{"display": showGrid, "color": gridColor}
	yGrid := map[string]any{"display": showGrid, "color": gridColor}
	cfg := map[string]any{
		"type": chartType,
		"data": map[string]any{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": map[string]any{
			"layout": map[string]any{
				"padding": map[string]any{
					"left": 8, "right": 12, "top": 8, "bottom": 4,
				},
			},
			"plugins": map[string]any{
				"legend": map[string]any{"display": true, "position": "top", "labels": map[string]any{"color": legendColor}},
				"title":  map[string]any{"display": true, "text": title, "color": titleColor},
				"datalabels": map[string]any{
					"display":   showLabels,
					"anchor":    "end",
					"align":     "end",
					"offset":    2,
					"font":      map[string]any{"size": 10},
					"formatter": "function(v){ if (v === null || v === undefined) return ''; var n = Number(v); if (!isFinite(n)) return ''; if (Math.abs(n) < 1e-6) return ''; if (Math.abs(n) < 0.01) return n.toPrecision(3); return n.toFixed(2); }",
				},
			},
			"scales": map[string]any{
				"x": map[string]any{
					"ticks": map[string]any{
						"autoSkip":      true,
						"maxTicksLimit": 8,
						"maxRotation":   0,
						"minRotation":   0,
						"color":         tickColor,
					},
					"grid": xGrid,
				},
				"y": map[string]any{
					"ticks": map[string]any{"maxTicksLimit": 6, "color": tickColor},
					"grid":  yGrid,
				},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	q := url.QueryEscape(string(b))
	return fmt.Sprintf("https://quickchart.io/chart?backgroundColor=%s&width=%d&height=%d&c=%s", bgColor, width, height, q)
}

func BuildCumulativeProfitChartURL(title string, labels []string, values []float64, unit, theme, size string, showLabels, showGrid bool) string {
	if len(labels) == 0 {
		return ""
	}
	dark := strings.EqualFold(strings.TrimSpace(theme), "dark")
	points := make([]map[string]any, 0, len(values))
	labelAlign := make([]string, 0, len(values))
	labelColors := make([]string, 0, len(values))
	for i, v := range values {
		diffLabel := ""
		align := "top"
		color := "#22c55e"
		if i > 0 {
			d := v - values[i-1]
			rounded := math.Round(d*100) / 100
			if math.Abs(rounded) >= 1e-9 {
				diffLabel = fmt.Sprintf("%+.2f", rounded)
				if rounded < 0 {
					align = "bottom"
					color = "#ef4444"
				}
			}
		}
		points = append(points, map[string]any{"y": v, "label": diffLabel})
		labelAlign = append(labelAlign, align)
		labelColors = append(labelColors, color)
	}
	titleColor := "#ffffff"
	legendColor := "#e5e7eb"
	tickColor := "#d1d5db"
	gridColor := "rgba(255,255,255,0.10)"
	lineColor := "#d9d9d9"
	fillColor := "rgba(217,217,217,0.18)"
	bgColor := "%23000000"
	if !dark {
		titleColor = "#111827"
		legendColor = "#111827"
		tickColor = "#374151"
		gridColor = "rgba(0,0,0,0.06)"
		lineColor = "#334155"
		fillColor = "rgba(51,65,85,0.18)"
		bgColor = "white"
	}
	width, height := chartDimensions(size)
	xGrid := map[string]any{"display": showGrid, "color": gridColor}
	yGrid := map[string]any{"display": showGrid, "color": gridColor}
	cfg := map[string]any{
		"type": "line",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{{
				"label": "Profit", "data": points,
				"borderColor": lineColor, "backgroundColor": fillColor,
				"pointRadius": 3, "pointHoverRadius": 4, "pointBackgroundColor": lineColor,
				"fill": false, "stepped": true, "tension": 0, "borderWidth": 2,
			}},
		},
		"options": map[string]any{
			"plugins": map[string]any{
				"title":      map[string]any{"display": true, "text": title, "color": titleColor, "font": map[string]any{"size": 20}},
				"legend":     map[string]any{"display": true, "labels": map[string]any{"color": legendColor}},
				"datalabels": map[string]any{"display": showLabels, "align": labelAlign, "anchor": "end", "offset": 4, "font": map[string]any{"size": 10, "weight": "bold"}, "color": labelColors},
			},
			"scales": map[string]any{
				"x": map[string]any{"ticks": map[string]any{"color": tickColor, "maxTicksLimit": 8}, "grid": xGrid, "title": map[string]any{"display": false}},
				"y": map[string]any{"ticks": map[string]any{"color": tickColor}, "grid": yGrid, "title": map[string]any{"display": true, "text": unit, "color": tickColor}},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	q := url.QueryEscape(string(b))
	return fmt.Sprintf("https://quickchart.io/chart?backgroundColor=%s&width=%d&height=%d&c=%s", bgColor, width, height, q)
}
