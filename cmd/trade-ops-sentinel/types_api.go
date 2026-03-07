package main

import (
	"fmt"
	"time"
)

type accountResponse struct {
	Balances []struct {
		Asset  string `json:"asset"`
		Free   string `json:"free"`
		Locked string `json:"locked"`
	} `json:"balances"`
}

type priceResponse struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol     string `json:"symbol"`
		Status     string `json:"status"`
		QuoteAsset string `json:"quoteAsset"`
		Filters    []struct {
			FilterType  string `json:"filterType"`
			MinNotional string `json:"minNotional"`
		} `json:"filters"`
	} `json:"symbols"`
}

type orderResponse struct {
	Symbol              string `json:"symbol"`
	OrderID             int64  `json:"orderId"`
	Status              string `json:"status"`
	ExecutedQty         string `json:"executedQty"`
	CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
	TransactTime        int64  `json:"transactTime"`
}

type myTrade struct {
	ID              int64  `json:"id"`
	OrderID         int64  `json:"orderId"`
	Price           string `json:"price"`
	Qty             string `json:"qty"`
	QuoteQty        string `json:"quoteQty"`
	IsBuyer         bool   `json:"isBuyer"`
	Commission      string `json:"commission"`
	CommissionAsset string `json:"commissionAsset"`
	Time            int64  `json:"time"`
	Symbol          string `json:"-"`
}

type binanceErrorResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type binanceAPIError struct {
	HTTPStatus int
	Code       int
	Msg        string
	BanUntil   time.Time
}

func (e *binanceAPIError) Error() string {
	if e.BanUntil.IsZero() {
		return fmt.Sprintf("binance http=%d code=%d msg=%s", e.HTTPStatus, e.Code, e.Msg)
	}
	return fmt.Sprintf("binance http=%d code=%d msg=%s", e.HTTPStatus, e.Code, e.Msg)
}
