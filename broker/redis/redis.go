// Package redis provides a small Redis Pub/Sub implementation of neo.EventBroker.
package redis

import internalredis "github.com/Protocol-Lattice/neo/internal/broker/redis"

const (
	DefaultAddr             = internalredis.DefaultAddr
	DefaultDialTimeout      = internalredis.DefaultDialTimeout
	DefaultHandshakeTimeout = internalredis.DefaultHandshakeTimeout
	DefaultSubscriberBuffer = internalredis.DefaultSubscriberBuffer
	DefaultMaxMessageBytes  = internalredis.DefaultMaxMessageBytes
)

type (
	Logger  = internalredis.Logger
	Options = internalredis.Options
	Broker  = internalredis.Broker
)

var New = internalredis.New
