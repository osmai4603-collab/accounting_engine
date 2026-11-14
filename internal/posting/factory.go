package posting

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
)

var ErrRequiredCapabilityUnavailable = errors.New("required posting capability is unavailable")

// Config declares which optional posting controls must be present for an
// engine instance to be built.
type Config struct {
	RequireApproval            bool
	RequireFinancialDimensions bool
	RequireMakerCheckerPolicy  bool
	RequirePolicy              bool
}

// Capabilities contains the optional controls that can be wired into a
// posting engine. A nil dependency means that capability is unavailable.
type Capabilities struct {
	ApprovalGate        ApprovalGate
	FinancialDimensions FinancialDimensionPolicy
	MakerCheckerPolicy  MakerCheckerPolicy
	PolicyUnit          *policy.Unit
}

// NewConfiguredEngine is the single constructor for posting engines. Required
// controls are validated before an engine can be used.
func NewConfiguredEngine(repo Repository, masterData *masterdata.Service, config Config, capabilities Capabilities) (*Engine, error) {
	var missing []string
	if config.RequireApproval && isNilCapability(capabilities.ApprovalGate) {
		missing = append(missing, "approval")
	}
	if config.RequireFinancialDimensions && isNilCapability(capabilities.FinancialDimensions) {
		missing = append(missing, "financial dimensions")
	}
	if config.RequireMakerCheckerPolicy && isNilCapability(capabilities.MakerCheckerPolicy) {
		missing = append(missing, "maker-checker policy")
	}
	if config.RequirePolicy && capabilities.PolicyUnit == nil {
		missing = append(missing, "posting policy")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrRequiredCapabilityUnavailable, joinCapabilityNames(missing))
	}

	engine := &Engine{repo: repo, masterdata: masterData}
	if !isNilCapability(capabilities.ApprovalGate) {
		engine.SetApprovalGate(capabilities.ApprovalGate)
	}
	if !isNilCapability(capabilities.FinancialDimensions) {
		engine.SetFinancialDimensionsEngine(NewFinancialDimensionsEngine(capabilities.FinancialDimensions))
	}
	if !isNilCapability(capabilities.MakerCheckerPolicy) {
		engine.SetMakerCheckerPolicy(capabilities.MakerCheckerPolicy)
	}
	if !isNilCapability(capabilities.PolicyUnit) {
		engine.SetPolicyUnit(capabilities.PolicyUnit)
	}
	return engine, nil
}

func isNilCapability(capability any) bool {
	if capability == nil {
		return true
	}
	value := reflect.ValueOf(capability)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func joinCapabilityNames(names []string) string {
	result := names[0]
	for _, name := range names[1:] {
		result += ", " + name
	}
	return result
}
