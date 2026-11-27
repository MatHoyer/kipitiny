package domain

import (
	"context"
	"time"
)

// NotificationRepository stores notification channels, email providers and push subscriptions.
type NotificationRepository interface {
	ListNotificationChannels(ctx context.Context) ([]NotificationChannel, error)
	GetNotificationChannel(ctx context.Context, id string) (NotificationChannel, error)
	CreateNotificationChannel(ctx context.Context, ch NotificationChannel) (NotificationChannel, error)
	UpdateNotificationChannel(ctx context.Context, ch NotificationChannel) (NotificationChannel, error)
	DeleteNotificationChannel(ctx context.Context, id string) error
	ListEmailProviders(ctx context.Context) ([]EmailProvider, error)
	GetEmailProvider(ctx context.Context, id string) (EmailProvider, error)
	CreateEmailProvider(ctx context.Context, p EmailProvider) (EmailProvider, error)
	UpdateEmailProvider(ctx context.Context, p EmailProvider) (EmailProvider, error)
	DeleteEmailProvider(ctx context.Context, id string) error
	// SavePushSubscription adds a browser, or moves its endpoint to p.UserID
	// with the new keys when it is already known.
	SavePushSubscription(ctx context.Context, p PushSubscription) (PushSubscription, error)
	ListPushSubscriptions(ctx context.Context, userID string) ([]PushSubscription, error)
	// ListAdminPushSubscriptions returns the browsers of every admin.
	ListAdminPushSubscriptions(ctx context.Context) ([]PushSubscription, error)
	DeletePushSubscription(ctx context.Context, userID, id string) error
}

// NotificationChannel is one destination for notifications. Config is
// specific to the kind (see internal/notify) and may hold secrets.
type NotificationChannel struct {
	ID     string            `bun:"id,pk" json:"id"`
	Name   string            `bun:"name" json:"name"`
	Kind   string            `bun:"kind" json:"kind"`
	Config map[string]string `bun:"config" json:"config"`
	// Events lists the event types sent to this channel.
	Events    []string  `bun:"events" json:"events"`
	Enabled   bool      `bun:"enabled" json:"enabled"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}

// EmailProvider is an SMTP server email goes out through.
type EmailProvider struct {
	ID       string `bun:"id,pk" json:"id"`
	Name     string `bun:"name" json:"name"`
	Host     string `bun:"host" json:"host"`
	Port     int    `bun:"port" json:"port"`
	Security string `bun:"security" json:"security"`
	Username string `bun:"username" json:"username"`
	Password string `bun:"password" json:"password"`
	// From is the sender, an address with an optional name.
	From      string    `bun:"sender" json:"from"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}
