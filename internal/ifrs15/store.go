package ifrs15

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrStoreNilContract = errors.New("cannot save nil or uninitialized contract")
	ErrStoreCompanyNil  = errors.New("company id is required for contract storage")
)

type ContractStatus string

const (
	ContractStatusDraft     ContractStatus = "Draft"
	ContractStatusActive    ContractStatus = "Active"
	ContractStatusModified  ContractStatus = "Modified"
	ContractStatusSatisfied ContractStatus = "Satisfied"
)

// ContractStore defines the persistence port for IFRS 15 contracts.
type ContractStore interface {
	SaveContract(ctx context.Context, companyID uuid.UUID, contract Contract) error
	GetContract(ctx context.Context, companyID, id uuid.UUID) (Contract, error)
	ListContracts(ctx context.Context, companyID uuid.UUID) ([]Contract, error)
	DeleteContract(ctx context.Context, companyID, id uuid.UUID) error
}

// InMemoryContractStore is a thread-safe in-memory implementation of ContractStore.
type InMemoryContractStore struct {
	mu        sync.RWMutex
	contracts map[uuid.UUID]map[uuid.UUID]Contract
}

func NewInMemoryContractStore() *InMemoryContractStore {
	return &InMemoryContractStore{
		contracts: make(map[uuid.UUID]map[uuid.UUID]Contract),
	}
}

func (s *InMemoryContractStore) SaveContract(ctx context.Context, companyID uuid.UUID, contract Contract) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if companyID == uuid.Nil {
		return ErrStoreCompanyNil
	}
	if err := contract.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	companyMap, exists := s.contracts[companyID]
	if !exists {
		companyMap = make(map[uuid.UUID]Contract)
		s.contracts[companyID] = companyMap
	}
	companyMap[contract.ID] = cloneContract(contract)
	return nil
}

func (s *InMemoryContractStore) GetContract(ctx context.Context, companyID, id uuid.UUID) (Contract, error) {
	if err := ctx.Err(); err != nil {
		return Contract{}, err
	}
	if companyID == uuid.Nil || id == uuid.Nil {
		return Contract{}, ErrStoreCompanyNil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	companyMap, exists := s.contracts[companyID]
	if !exists {
		return Contract{}, ErrContractNotFound
	}
	c, found := companyMap[id]
	if !found {
		return Contract{}, ErrContractNotFound
	}
	return cloneContract(c), nil
}

func (s *InMemoryContractStore) ListContracts(ctx context.Context, companyID uuid.UUID) ([]Contract, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil {
		return nil, ErrStoreCompanyNil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	companyMap, exists := s.contracts[companyID]
	if !exists {
		return []Contract{}, nil
	}
	results := make([]Contract, 0, len(companyMap))
	for _, c := range companyMap {
		results = append(results, cloneContract(c))
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].ID.String() < results[j].ID.String()
	})
	return results, nil
}

func (s *InMemoryContractStore) DeleteContract(ctx context.Context, companyID, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if companyID == uuid.Nil || id == uuid.Nil {
		return ErrStoreCompanyNil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	companyMap, exists := s.contracts[companyID]
	if !exists {
		return ErrContractNotFound
	}
	if _, found := companyMap[id]; !found {
		return ErrContractNotFound
	}
	delete(companyMap, id)
	return nil
}

