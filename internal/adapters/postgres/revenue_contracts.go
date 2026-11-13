package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs15"
)

var (
	ErrContractNotFound = errors.New("revenue contract not found")
)

func (d *Driver) SaveContract(ctx context.Context, companyID uuid.UUID, contract ifrs15.Contract) error {
	if companyID == uuid.Nil {
		return errors.New("company id is required to save revenue contract")
	}
	if err := contract.Validate(); err != nil {
		return fmt.Errorf("validate revenue contract: %w", err)
	}

	payload, err := json.Marshal(contract)
	if err != nil {
		return fmt.Errorf("marshal revenue contract: %w", err)
	}

	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO ifrs15_revenue_contracts(
				id, company_id, customer_id, currency, transaction_price, contract_data, updated_at
			) VALUES($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (company_id, id) DO UPDATE SET
				customer_id = EXCLUDED.customer_id,
				currency = EXCLUDED.currency,
				transaction_price = EXCLUDED.transaction_price,
				contract_data = EXCLUDED.contract_data,
				updated_at = EXCLUDED.updated_at
		`, contract.ID, companyID, contract.CustomerID, contract.Currency,
			contract.TransactionPrice.Amount(), payload, time.Now().UTC())
		return err
	})
}

func (d *Driver) ListContracts(ctx context.Context, companyID uuid.UUID) ([]ifrs15.Contract, error) {
	if companyID == uuid.Nil {
		return nil, errors.New("company id is required to list revenue contracts")
	}

	contracts := make([]ifrs15.Contract, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT contract_data FROM ifrs15_revenue_contracts
			WHERE company_id = $1
			ORDER BY created_at ASC
		`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var payload []byte
			if err := rows.Scan(&payload); err != nil {
				return err
			}
			var contract ifrs15.Contract
			if err := json.Unmarshal(payload, &contract); err != nil {
				return fmt.Errorf("unmarshal revenue contract: %w", err)
			}
			contracts = append(contracts, contract)
		}
		return rows.Err()
	})
	return contracts, err
}

func (d *Driver) GetContract(ctx context.Context, companyID, id uuid.UUID) (ifrs15.Contract, error) {
	var contract ifrs15.Contract
	if companyID == uuid.Nil || id == uuid.Nil {
		return contract, errors.New("company id and contract id are required")
	}

	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var payload []byte
		err := tx.QueryRow(ctx, `
			SELECT contract_data FROM ifrs15_revenue_contracts
			WHERE company_id = $1 AND id = $2
		`, companyID, id).Scan(&payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrContractNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(payload, &contract)
	})
	return contract, err
}

func (d *Driver) DeleteContract(ctx context.Context, companyID, id uuid.UUID) error {
	if companyID == uuid.Nil || id == uuid.Nil {
		return errors.New("company id and contract id are required")
	}

	return d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM ifrs15_revenue_contracts
			WHERE company_id = $1 AND id = $2
		`, companyID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrContractNotFound
		}
		return nil
	})
}
