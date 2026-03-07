package domain

import (
	"errors"
	"fmt"
)

type ThresholdPolicy struct {
	MinBNB         float64
	TargetBNB      float64
	MinBNBUSDT     float64
	TargetBNBUSDT  float64
	BNBRatioMode   bool
	BNBRatioMin    float64
	BNBRatioTarget float64
	QuoteAsset     string
}

func (p ThresholdPolicy) UseUSDTThresholds() bool {
	return p.MinBNBUSDT > 0 || p.TargetBNBUSDT > 0
}

func (p ThresholdPolicy) UseRatioThresholds() bool {
	return p.BNBRatioMode
}

func ResolveBNBThresholds(price, portfolioQuote float64, p ThresholdPolicy) (float64, float64, error) {
	if price <= 0 {
		return 0, 0, errors.New("invalid symbol price for threshold conversion")
	}
	if p.UseRatioThresholds() {
		if portfolioQuote <= 0 {
			return 0, 0, errors.New("invalid portfolio value for ratio threshold conversion")
		}
		minBNB := (portfolioQuote * p.BNBRatioMin) / price
		targetBNB := (portfolioQuote * p.BNBRatioTarget) / price
		return minBNB, targetBNB, nil
	}
	if p.UseUSDTThresholds() {
		minBNB := p.MinBNBUSDT / price
		targetBNB := p.TargetBNBUSDT / price
		return minBNB, targetBNB, nil
	}
	return p.MinBNB, p.TargetBNB, nil
}

func ThresholdModeLine(p ThresholdPolicy) string {
	if p.UseRatioThresholds() {
		return fmt.Sprintf("Threshold ratio=%.4f%%, Target ratio=%.4f%% of portfolio", p.BNBRatioMin*100, p.BNBRatioTarget*100)
	}
	if p.UseUSDTThresholds() {
		return fmt.Sprintf("Threshold=%s %.4f (~auto BNB), Target=%s %.4f (~auto BNB)", p.QuoteAsset, p.MinBNBUSDT, p.QuoteAsset, p.TargetBNBUSDT)
	}
	return fmt.Sprintf("Threshold=%.6f BNB, Target=%.6f BNB", p.MinBNB, p.TargetBNB)
}
