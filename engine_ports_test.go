package accounting_engine

import (
	"errors"
	"testing"

	"accounting_engine/storage/memory"
)

func TestNewWithPortsAcceptsIndependentStoragePorts(t *testing.T) {
	ports := StoragePorts{
		Posting:    memory.New(),
		Reporting:  memory.New(),
		MasterData: memory.New(),
		Subledger:  memory.New(),
		Governance: memory.New(),
		Valuation:  memory.New(),
	}

	engine, err := NewWithPorts(ports, DefaultConfig())
	if err != nil {
		t.Fatalf("construct engine with domain-specific ports: %v", err)
	}
	if engine.MasterData == nil || engine.Posting == nil || engine.Subledgers == nil ||
		engine.Valuation == nil || engine.Reporting == nil || engine.Governance == nil {
		t.Fatal("engine did not initialize every accounting domain")
	}
}

func TestNewWithPortsRequiresEveryPort(t *testing.T) {
	if _, err := NewWithPorts(StoragePorts{}, DefaultConfig()); !errors.Is(err, ErrStoragePortRequired) {
		t.Fatalf("NewWithPorts error = %v, want ErrStoragePortRequired", err)
	}
}
