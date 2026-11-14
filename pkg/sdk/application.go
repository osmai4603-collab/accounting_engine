package sdk

import (
	"fmt"

	"github.com/osmai4603-collab/accounting_engine/internal/sdkapplication"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

func NewApplicationService(ports storage.Ports) (publicapplication.Service, error) {
	return NewApplicationServiceWithAuthorizer(ports, nil)
}

func NewApplicationServiceWithAuthorizer(
	ports storage.Ports,
	authorizer publicapplication.Authorizer,
) (publicapplication.Service, error) {
	return NewApplicationServiceWithPostingConfig(ports, PostingConfig{}, authorizer)
}

// NewApplicationServiceWithPostingConfig builds the application service only
// when all configured posting controls can be wired.
func NewApplicationServiceWithPostingConfig(
	ports storage.Ports,
	config PostingConfig,
	authorizer publicapplication.Authorizer,
) (publicapplication.Service, error) {
	return NewApplicationServiceWithPostingCapabilities(ports, config, storage.PostingCapabilities{}, authorizer)
}

// NewApplicationServiceWithPostingCapabilities allows an adapter to supply
// the controls required by PostingConfig.
func NewApplicationServiceWithPostingCapabilities(
	ports storage.Ports,
	config PostingConfig,
	capabilities storage.PostingCapabilities,
	authorizer publicapplication.Authorizer,
) (publicapplication.Service, error) {
	app, err := sdkapplication.NewApplicationFromPortsWithPostingCapabilities(ports, config.internal(), capabilities)
	if err != nil {
		return nil, fmt.Errorf("application layer: %w", err)
	}
	return sdkapplication.NewPublicService(app, authorizer), nil
}
