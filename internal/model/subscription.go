package model

import (
	"time"

	"common-vpn-router/internal/node"
)

type Subscription struct {
	ID                  string      `json:"id"`
	Name                string      `json:"name"`
	URL                 string      `json:"url"`
	Nodes               []node.Node `json:"nodes"`
	UpdatedAt           time.Time   `json:"updatedAt,omitempty"`
	UpdateIntervalHours int         `json:"updateIntervalHours,omitempty"`
	ETag                string      `json:"etag,omitempty"`
	LastModified        string      `json:"lastModified,omitempty"`
}
