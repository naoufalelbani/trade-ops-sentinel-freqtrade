package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite"
)

func newTradeStore(cfg Config) (*TradeStore, error) {
	if !cfg.SQLiteEnabled {
		return nil, nil
	}
	db, err := sql.Open("sqlite", cfg.SQLitePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS trades (
  symbol TEXT NOT NULL,
  trade_id INTEGER NOT NULL,
  order_id INTEGER NOT NULL,
  side TEXT NOT NULL,
  price REAL NOT NULL,
  qty REAL NOT NULL,
  quote_qty REAL NOT NULL,
  commission REAL NOT NULL,
  commission_asset TEXT NOT NULL,
  trade_time INTEGER NOT NULL,
  PRIMARY KEY (symbol, trade_id)
);
CREATE INDEX IF NOT EXISTS idx_trades_symbol_time ON trades(symbol, trade_time);
CREATE INDEX IF NOT EXISTS idx_trades_time ON trades(trade_time);
CREATE TABLE IF NOT EXISTS trade_sync_state (
  symbol TEXT PRIMARY KEY,
  last_synced_time INTEGER NOT NULL
);
`); err != nil {
		_ = db.Close()
		return nil, err
	}
	log.Printf("sqlite trade store enabled path=%s", cfg.SQLitePath)
	return &TradeStore{
		db:                  db,
		initialLookbackDays: cfg.SQLiteInitialLookbackDays,
		syncInterval:        cfg.SQLiteSyncInterval,
		maxLookbackDays:     cfg.SQLiteMaxLookbackDays,
	}, nil
}

func resolveTrackedSymbols(ctx context.Context, cfg Config, binance *BinanceClient) ([]string, error) {
	if len(cfg.TrackedSymbols) == 1 && cfg.TrackedSymbols[0] == "FREQTRADE" {
		syms, err := resolveTrackedSymbolsFromFreqtrade(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if len(syms) == 0 {
			return nil, errors.New("freqtrade returned no tracked pairs")
		}
		return syms, nil
	}
	if len(cfg.TrackedSymbols) == 1 && cfg.TrackedSymbols[0] == "ALL" {
		syms, err := binance.ListTradingSymbolsByQuote(ctx, cfg.QuoteAsset)
		if err != nil {
			return nil, err
		}
		if cfg.MaxAutoTrackedSymbols > 0 && len(syms) > cfg.MaxAutoTrackedSymbols {
			syms = syms[:cfg.MaxAutoTrackedSymbols]
		}
		return syms, nil
	}
	return cfg.TrackedSymbols, nil
}

func newRedisCache(cfg Config) (*RedisCache, error) {
	if !cfg.RedisEnabled {
		return &RedisCache{enabled: false}, nil
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	log.Printf("redis cache enabled addr=%s db=%d", cfg.RedisAddr, cfg.RedisDB)
	return &RedisCache{enabled: true, client: client, prefix: cfg.RedisKeyPrefix}, nil
}

func (c *RedisCache) key(raw string) string {
	return c.prefix + raw
}

func (c *RedisCache) getJSON(ctx context.Context, key string, dst any) (bool, error) {
	if c == nil || !c.enabled {
		return false, nil
	}
	v, err := c.client.Get(ctx, c.key(key)).Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal([]byte(v), dst); err != nil {
		return false, err
	}
	return true, nil
}

func (c *RedisCache) setJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c == nil || !c.enabled {
		return nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, c.key(key), b, ttl).Err()
}
