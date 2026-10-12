package mongodb

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// function to validate mongo db connection url
type MongoDBConnection struct {
	ConnectionURI string
	Client        *mongo.Client
}

// TODO - validate the connection url
func NewMongoDBConnection(connectionURI string) *MongoDBConnection {

	return &MongoDBConnection{
		ConnectionURI: connectionURI,
	}
}

// function to setup and pool db connection - and to close connectoin after the dispatcher closes down
func (c *MongoDBConnection) SetupConnection(ctx context.Context) error {
	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(c.ConnectionURI))

	if err != nil {
		return err
	}

	c.Client = client

	return nil
}

func (c *MongoDBConnection) CloseConnection(ctx context.Context) error {
	if err := c.Client.Disconnect(ctx); err != nil {
		return err
	}

	return nil
}

// function to store jobs in db

// function to fetch job from db

// implement the interface
