package main

import (
	"log"
	"time"
)

func logTiming(name string, started time.Time) {
	log.Printf("timing op=%s duration_ms=%d", name, time.Since(started).Milliseconds())
}

func logIfErr(op string, err error) {
	if err != nil {
		log.Printf("error op=%s err=%v", op, err)
	}
}
