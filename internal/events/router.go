package events

import (
	apiEvents "accounting_engine/pkg/api/v1/events"
)

type PostingRule = apiEvents.PostingRule
type Router = apiEvents.Router

func NewRouter() *Router {
	return apiEvents.NewRouter()
}
