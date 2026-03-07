package main

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

func formatSignedNoPlus(v float64, prec int) string {
	if math.Abs(v) < 1e-12 {
		v = 0
	}
	return strconv.FormatFloat(v, 'f', prec, 64)
}

func trimNum(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0"
	}
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

func symbolsCacheKey(symbols []string) string {
	cp := append([]string(nil), symbols...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}
