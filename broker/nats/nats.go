// Package nats provides a small NATS-backed implementation of neo.EventBroker.
package nats

import internalnats "github.com/Protocol-Lattice/neo/internal/broker/nats"

const (
	DefaultAddr             = internalnats.DefaultAddr
	DefaultDialTimeout      = internalnats.DefaultDialTimeout
	DefaultHandshakeTimeout = internalnats.DefaultHandshakeTimeout
	DefaultSubscriberBuffer = internalnats.DefaultSubscriberBuffer
	DefaultMaxMessageBytes  = internalnats.DefaultMaxMessageBytes
)

type (
	Logger  = internalnats.Logger
	Options = internalnats.Options
	Broker  = internalnats.Broker
)

var New = internalnats.New
