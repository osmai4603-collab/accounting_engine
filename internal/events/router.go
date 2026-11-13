package events

import (
	apiEvents "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
)

type PostingRule = apiEvents.PostingRule
type Router = apiEvents.Router

func NewRouter() *Router {
	return apiEvents.NewRouter()
}
