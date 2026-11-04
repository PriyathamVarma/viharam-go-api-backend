package mongodb

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Database owns the shared MongoDB client and database handle used by the
// Viharam domain modules. The client is safe for concurrent use.
type Database struct {
	Client *mongo.Client
	DB     *mongo.Database
}

func Connect(ctx context.Context, uri, name string) (*Database, error) {
	clientOpts := options.Client().ApplyURI(uri).SetServerSelectionTimeout(5 * time.Second)
	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return &Database{Client: client, DB: client.Database(name)}, nil
}

func (d *Database) Ping(ctx context.Context) error { return d.Client.Ping(ctx, nil) }

func (d *Database) Close(ctx context.Context) error { return d.Client.Disconnect(ctx) }

func (d *Database) Collection(name string) *mongo.Collection { return d.DB.Collection(name) }
