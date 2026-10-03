package subscription

import (
	"common-vpn-router/internal/model"
	"common-vpn-router/internal/node"
)

type Subscription = model.Subscription

type Parsed struct {
	Nodes []node.Node
	Name  string
}
