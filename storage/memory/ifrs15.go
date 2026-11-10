package memory

import (
	"context"
	"errors"
	"sort"

	"accounting_engine/internal/ifrs15"

	"github.com/google/uuid"
)

func (d *Driver) SaveContract(ctx context.Context, companyID uuid.UUID, contract ifrs15.Contract) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if companyID == uuid.Nil {
		return errors.New("company id is required to save revenue contract")
	}
	if err := contract.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.contracts[companyID] == nil {
		d.contracts[companyID] = make(map[uuid.UUID]ifrs15.Contract)
	}
	d.contracts[companyID][contract.ID] = contract
	return nil
}

func (d *Driver) ListContracts(ctx context.Context, companyID uuid.UUID) ([]ifrs15.Contract, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil {
		return nil, errors.New("company id is required to list revenue contracts")
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	store, ok := d.contracts[companyID]
	if !ok {
		return []ifrs15.Contract{}, nil
	}
	results := make([]ifrs15.Contract, 0, len(store))
	for _, c := range store {
		results = append(results, c)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].ID.String() < results[j].ID.String()
	})
	return results, nil
}

func (d *Driver) GetContract(ctx context.Context, companyID, id uuid.UUID) (ifrs15.Contract, error) {
	if err := ctx.Err(); err != nil {
		return ifrs15.Contract{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if store, ok := d.contracts[companyID]; ok {
		if c, found := store[id]; found {
			return c, nil
		}
	}
	return ifrs15.Contract{}, errors.New("revenue contract not found")
}

func (d *Driver) DeleteContract(ctx context.Context, companyID, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if store, ok := d.contracts[companyID]; ok {
		if _, found := store[id]; found {
			delete(store, id)
			return nil
		}
	}
	return errors.New("revenue contract not found")
}
