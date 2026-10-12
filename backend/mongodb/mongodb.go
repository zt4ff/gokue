// Package mongodb provides a MongoDB-backed implementation of the
// backend.JobStore interface.
package mongodb

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
)

// MongoDBConnection wraps a MongoDB client and the URI used to dial it.
type MongoDBConnection struct {
	// ConnectionURI is the MongoDB connection string.
	ConnectionURI string
	// Client is the underlying client, populated by SetupConnection.
	Client *mongo.Client
}

// NewMongoDBConnection validates connectionURI and returns a connection handle.
// It does not dial the server; call SetupConnection to establish the client.
func NewMongoDBConnection(connectionURI string) (*MongoDBConnection, error) {
	if connectionURI == "" {
		return nil, errors.New("mongodb connection uri cannot be empty")
	}
	if _, err := connstring.ParseAndValidate(connectionURI); err != nil {
		return nil, fmt.Errorf("invalid mongodb connection uri: %w", err)
	}

	return &MongoDBConnection{ConnectionURI: connectionURI}, nil
}

// SetupConnection dials the server and verifies reachability with a ping.
func (c *MongoDBConnection) SetupConnection(ctx context.Context) error {
	if c == nil {
		return errors.New("mongodb connection is nil")
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(c.ConnectionURI))
	if err != nil {
		return fmt.Errorf("connect to mongodb: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return fmt.Errorf("ping mongodb: %w", err)
	}

	c.Client = client
	return nil
}

// CloseConnection disconnects the client. It is nil-safe and idempotent.
func (c *MongoDBConnection) CloseConnection(ctx context.Context) error {
	if c == nil || c.Client == nil {
		return nil
	}

	err := c.Client.Disconnect(ctx)
	c.Client = nil
	if err != nil {
		return fmt.Errorf("disconnect mongodb: %w", err)
	}
	return nil
}
