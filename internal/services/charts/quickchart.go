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
		gridColor = "rgba(255,255,255,0.38)"
	}
	width, height := chartDimensions(size)

	xGrid := map[string]any{"display": showGrid, "color": gridColor, "lineWidth": 1.1, "drawBorder": false}
	yGrid := map[string]any{"display": showGrid, "color": gridColor, "lineWidth": 1.1, "drawBorder": false}
	cfg := map[string]any{
		"type": chartType,
		"data": map[string]any{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": map[string]any{
			"layout": map[string]any{
				"padding": map[string]any{
					"left": 8, "right": 20, "top": 8, "bottom": 4,
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
					"offset": true,
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
				// Legacy Chart.js v2 fallback used by some QuickChart render paths.
				"xAxes": []map[string]any{{
					"offset": true,
					"ticks": map[string]any{
						"autoSkip":      true,
						"maxTicksLimit": 8,
						"maxRotation":   0,
						"minRotation":   0,
						"fontColor":     tickColor,
					},
					"gridLines": map[string]any{
						"display":    showGrid,
						"color":      gridColor,
						"lineWidth":  1.1,
						"drawBorder": false,
					},
				}},
				"yAxes": []map[string]any{{
					"ticks": map[string]any{
						"maxTicksLimit": 6,
						"fontColor":     tickColor,
					},
					"gridLines": map[string]any{
						"display":    showGrid,
						"color":      gridColor,
						"lineWidth":  1.1,
						"drawBorder": false,
					},
				}},
			},
			// Legacy Chart.js title fallback for environments that ignore plugins.title.
			"title":  map[string]any{"display": true, "text": title, "fontColor": titleColor},
			"legend": map[string]any{"display": true, "position": "top", "labels": map[string]any{"fontColor": legendColor}},
		},
	}
	b, _ := json.Marshal(cfg)
	q := url.QueryEscape(string(b))
	return fmt.Sprintf("https://quickchart.io/chart?backgroundColor=%s&width=%d&height=%d&c=%s", bgColor, width, height, q)
}

func BuildCumulativeProfitChartURL(title string, labels []string, values []float64, unit, theme, size, labelMode string, showLabels, showGrid bool) string {
	if len(labels) == 0 {
		return ""
	}
	dark := strings.EqualFold(strings.TrimSpace(theme), "dark")
	points := make([]map[string]any, 0, len(values))
	labelAlign := make([]string, 0, len(values))
	labelColors := make([]string, 0, len(values))
	labelOffsets := make([]int, 0, len(values))

	mode := strings.ToLower(strings.TrimSpace(labelMode))
	if mode == "" {
		mode = "staggered"
	}

	// Staggering logic (only for "staggered" mode)
	lastAlign := ""
	staggerStep := 0
	densityFactor := 1
	if len(values) > 50 {
		densityFactor = 2
	}
	if len(values) > 100 {
		densityFactor = 3
	}

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

		offset := 4
		if mode == "staggered" {
			if i > 0 && align == lastAlign {
				staggerStep = (staggerStep + 1) % (densityFactor + 1)
			} else {
				staggerStep = 0
			}
			offset = 4 + (staggerStep * 12)
		}

		points = append(points, map[string]any{"y": v, "label": diffLabel})
		labelAlign = append(labelAlign, align)
		labelColors = append(labelColors, color)
		labelOffsets = append(labelOffsets, offset)
		lastAlign = align
	}

	titleColor := "#ffffff"
	legendColor := "#e5e7eb"
	tickColor := "#d1d5db"
	gridColor := "rgba(255,255,255,0.26)"
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
	xGrid := map[string]any{"display": showGrid, "color": gridColor, "lineWidth": 1.1, "drawBorder": false}
	yGrid := map[string]any{"display": showGrid, "color": gridColor, "lineWidth": 1.1, "drawBorder": false}

	rotation := 0
	topPadding := 8
	if mode == "vertical" {
		rotation = -90
		topPadding = 40
	} else if mode == "staggered" {
		topPadding = 20
	}

	offsetsJSON, _ := json.Marshal(labelOffsets)
	alignsJSON, _ := json.Marshal(labelAlign)
	colorsJSON, _ := json.Marshal(labelColors)

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
		"plugins": []any{
			map[string]any{
				"id": "staggerLines",
				"afterDatasetsDraw": fmt.Sprintf(`function(chart) {
					const ctx = chart.ctx;
					const mode = "%s";
					if (mode !== "staggered") return;
					const meta = chart.getDatasetMeta(0);
					const offsets = %s;
					const aligns = %s;
					const colors = %s;
					ctx.save();
					ctx.setLineDash([2, 2]);
					ctx.lineWidth = 1;
					meta.data.forEach((point, i) => {
						const offset = offsets[i];
						if (offset <= 6) return;
						ctx.strokeStyle = colors[i] || '#666';
						ctx.beginPath();
						const align = aligns[i];
						const startY = point.y;
						const endY = (align === 'top') ? (point.y - offset + 2) : (point.y + offset - 2);
						ctx.moveTo(point.x, startY);
						ctx.lineTo(point.x, endY);
						ctx.stroke();
					});
					ctx.restore();
				}`, mode, string(offsetsJSON), string(alignsJSON), string(colorsJSON)),
			},
		},
		"options": map[string]any{
			"layout": map[string]any{
				"padding": map[string]any{
					"left": 10, "right": 44, "top": topPadding, "bottom": 6,
				},
			},
			"plugins": map[string]any{
				"title":      map[string]any{"display": true, "text": title, "color": titleColor, "font": map[string]any{"size": 20}},
				"legend":     map[string]any{"display": true, "labels": map[string]any{"color": legendColor}},
				"datalabels": map[string]any{
					"display":  showLabels,
					"overlap":  true,
					"align":    labelAlign,
					"anchor":   "end",
					"offset":   labelOffsets,
					"rotation": rotation,
					"font":     map[string]any{"size": 10, "weight": "bold"},
					"color":    labelColors,
				},
			},
			"scales": map[string]any{
				"x": map[string]any{"offset": true, "ticks": map[string]any{"color": tickColor, "maxTicksLimit": 8}, "grid": xGrid, "title": map[string]any{"display": false}},
				"y": map[string]any{"ticks": map[string]any{"color": tickColor}, "grid": yGrid, "title": map[string]any{"display": true, "text": unit, "color": tickColor}},
				// Legacy Chart.js v2 fallback used by some QuickChart render paths.
				"xAxes": []map[string]any{{
					"offset": true,
					"ticks": map[string]any{
						"maxTicksLimit": 8,
						"fontColor":     tickColor,
					},
					"gridLines": map[string]any{
						"display":    showGrid,
						"color":      gridColor,
						"lineWidth":  1.1,
						"drawBorder": false,
					},
				}},
				"yAxes": []map[string]any{{
					"ticks": map[string]any{
						"fontColor": tickColor,
					},
					"gridLines": map[string]any{
						"display":    showGrid,
						"color":      gridColor,
						"lineWidth":  1.1,
						"drawBorder": false,
					},
				}},
			},
			"title":  map[string]any{"display": true, "text": title, "fontColor": titleColor},
			"legend": map[string]any{"display": true, "labels": map[string]any{"fontColor": legendColor}},
		},
	}
	b, _ := json.Marshal(cfg)
	q := url.QueryEscape(string(b))
	return fmt.Sprintf("https://quickchart.io/chart?backgroundColor=%s&width=%d&height=%d&c=%s", bgColor, width, height, q)
}

func BuildForecastChartURL(title string, labels []string, history, forecast []float64, unit, theme, size string, showGrid bool) string {
	if len(labels) == 0 || len(history) == 0 || len(forecast) == 0 {
		return ""
	}
	dark := strings.EqualFold(strings.TrimSpace(theme), "dark")
	titleColor := "#ffffff"
	legendColor := "#e5e7eb"
	tickColor := "#d1d5db"
	gridColor := "rgba(255,255,255,0.26)"
	bgColor := "%23000000"
	historyColor := "#38bdf8"
	forecastColor := "#f59e0b"
	if !dark {
		titleColor = "#111827"
		legendColor = "#111827"
		tickColor = "#374151"
		gridColor = "rgba(0,0,0,0.06)"
		bgColor = "white"
		historyColor = "#0284c7"
		forecastColor = "#d97706"
	}
	width, height := chartDimensions(size)
	xGrid := map[string]any{"display": showGrid, "color": gridColor, "lineWidth": 1.1, "drawBorder": false}
	yGrid := map[string]any{"display": showGrid, "color": gridColor, "lineWidth": 1.1, "drawBorder": false}
	cfg := map[string]any{
		"type": "line",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{
				{
					"label":           "History",
					"data":            nullableSeries(history),
					"borderColor":     historyColor,
					"backgroundColor": "rgba(56,189,248,0.16)",
					"borderWidth":     2,
					"pointRadius":     2,
					"fill":            false,
					"tension":         0.18,
				},
				{
					"label":           "Forecast",
					"data":            nullableSeries(forecast),
					"borderColor":     forecastColor,
					"backgroundColor": "rgba(245,158,11,0.16)",
					"borderWidth":     2,
					"pointRadius":     2,
					"fill":            false,
					"tension":         0.18,
					"borderDash":      []int{8, 6},
				},
			},
		},
		"options": map[string]any{
			"layout": map[string]any{
				"padding": map[string]any{"left": 8, "right": 20, "top": 8, "bottom": 4},
			},
			"plugins": map[string]any{
				"title":  map[string]any{"display": true, "text": title, "color": titleColor},
				"legend": map[string]any{"display": true, "position": "top", "labels": map[string]any{"color": legendColor}},
			},
			"scales": map[string]any{
				"x": map[string]any{
					"offset": true,
					"ticks":  map[string]any{"color": tickColor, "maxTicksLimit": 10, "maxRotation": 0},
					"grid":   xGrid,
				},
				"y": map[string]any{
					"ticks": map[string]any{"color": tickColor},
					"grid":  yGrid,
					"title": map[string]any{"display": true, "text": unit, "color": tickColor},
				},
			},
			"title":  map[string]any{"display": true, "text": title, "fontColor": titleColor},
			"legend": map[string]any{"display": true, "position": "top", "labels": map[string]any{"fontColor": legendColor}},
		},
	}
	b, _ := json.Marshal(cfg)
	q := url.QueryEscape(string(b))
	return fmt.Sprintf("https://quickchart.io/chart?backgroundColor=%s&width=%d&height=%d&c=%s", bgColor, width, height, q)
}

func nullableSeries(values []float64) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			out = append(out, nil)
			continue
		}
		out = append(out, v)
	}
	return out
}
