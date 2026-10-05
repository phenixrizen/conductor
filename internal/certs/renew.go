package certs

import (
	"crypto/x509"
	"time"
)

// renewAt is when a certificate should be renewed: thirty days before it
// expires for one that lasts longer than ninety days, and at two thirds of
// its lifetime otherwise (a six-day certificate renews after four), never
// before now. The CA's own suggestion (ARI) can move it earlier.
func renewAt(leaf *x509.Certificate, now time.Time) time.Time {
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	var at time.Time
	if lifetime > 90*24*time.Hour {
		at = leaf.NotAfter.Add(-30 * 24 * time.Hour)
	} else {
		at = leaf.NotBefore.Add(lifetime * 2 / 3)
	}
	if at.Before(now) {
		return now
	}
	return at
}
