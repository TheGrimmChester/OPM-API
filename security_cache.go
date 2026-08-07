package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	opencache "github.com/TheGrimmChester/open-cache-go"
)

const peerSCMEventDedupTTL = 24 * time.Hour

var secCache *opencache.Layered

func initSecurityCache() {
	n := 20000
	if v := strings.TrimSpace(os.Getenv("OPM_SEC_L1_CACHE")); v != "" {
		if parsed, err := fmt.Sscanf(v, "%d", &n); err != nil || parsed != 1 {
			n = 20000
		}
	}
	if n < 1000 {
		n = 1000
	}
	prefix := strings.TrimSpace(envOr("OPM_SEC_KEY_PREFIX", "opm:sec:"))
	lc, err := opencache.NewLayered(opencache.Config{RedisURL: os.Getenv("REDIS_URL"), L1Max: n, KeyPrefix: prefix})
	if err != nil {
		log.Printf("[WARN] security cache: %v; memory-only", err)
		lc, _ = opencache.NewLayered(opencache.Config{L1Max: n, KeyPrefix: prefix})
	}
	secCache = lc
}

func peerSCMEventDedupSeen(ctx context.Context, scmJobID, eventID string) bool {
	if secCache == nil {
		return false
	}
	if scmJobID == "" && eventID == "" {
		return false
	}
	key := "scm-event:" + opencache.HashKey(scmJobID, eventID)
	return !secCache.SetNX(ctx, key, []byte("1"), peerSCMEventDedupTTL)
}

func jobTokenRedisKey(product, jti string) string {
	return "jobtok:" + opencache.HashKey(product, jti)
}

func allowLocalJobToken(ctx context.Context, product, jti string, ttl time.Duration) {
	if secCache == nil || jti == "" {
		return
	}
	secCache.SetPlain(ctx, jobTokenRedisKey(product, jti), []byte("1"), ttl)
}

func revokeLocalJobToken(ctx context.Context, product, jti string) {
	if secCache == nil || jti == "" {
		return
	}
	secCache.Del(ctx, jobTokenRedisKey(product, jti))
}
