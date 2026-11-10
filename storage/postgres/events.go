package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) SaveEvent(ctx context.Context, event subledgers.EconomicEvent) error {
	if event.CompanyID == uuid.Nil {
		return subledgers.ErrCompanyMismatch
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, event.CompanyID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO subledger_events(company_id,idempotency_key,event) VALUES($1,$2,$3)
			 ON CONFLICT(company_id,idempotency_key) DO NOTHING`,
			event.CompanyID, event.IdempotencyKey, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return subledgers.ErrAlreadyExists
		}
		return nil
	})
}

func (d *Driver) SaveEventAccountingRule(ctx context.Context, rule subledgers.EventAccountingRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	if rule.CompanyID == uuid.Nil {
		return subledgers.ErrCompanyMismatch
	}
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	data, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, rule.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO event_accounting_rules(id,company_id,event_type,priority,rule_data)
			 VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT(company_id,event_type,priority) DO UPDATE
			 SET id=EXCLUDED.id,rule_data=EXCLUDED.rule_data`,
			rule.ID, rule.CompanyID, rule.EventType, rule.Priority, data)
		return err
	})
}

func (d *Driver) ListEventAccountingRulesForCompany(ctx context.Context, companyID uuid.UUID, eventType string) ([]subledgers.EventAccountingRule, error) {
	rules := make([]subledgers.EventAccountingRule, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT rule_data FROM event_accounting_rules
			 WHERE company_id=$1 AND event_type=$2 ORDER BY priority DESC, id`,
			companyID, eventType)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var rule subledgers.EventAccountingRule
			if err := json.Unmarshal(data, &rule); err != nil {
				return err
			}
			rules = append(rules, rule)
		}
		return rows.Err()
	})
	return rules, err
}

func (d *Driver) SaveEventAndDraft(ctx context.Context, event subledgers.EconomicEvent, entry posting.JournalEntry) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if entry.ID == uuid.Nil || entry.CompanyID != event.CompanyID ||
		entry.ID != event.ResultEntryID || entry.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	tx, err := d.beginCompanyTx(ctx, event.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := d.saveEntryTx(ctx, tx, entry); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx,
		`INSERT INTO subledger_events(company_id,idempotency_key,event)
		 VALUES($1,$2,$3) ON CONFLICT(company_id,idempotency_key) DO NOTHING`,
		event.CompanyID, event.IdempotencyKey, data)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return subledgers.ErrAlreadyExists
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetEventByIdempotencyKey(ctx context.Context, key string) (subledgers.EconomicEvent, bool, error) {
	return subledgers.EconomicEvent{}, false, subledgers.ErrCompanyScopedEventsRequired
}

func (d *Driver) GetEventByIdempotencyKeyForCompany(ctx context.Context, companyID uuid.UUID, key string) (subledgers.EconomicEvent, bool, error) {
	var event subledgers.EconomicEvent
	found := false
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx,
			`SELECT event FROM subledger_events WHERE company_id=$1 AND idempotency_key=$2`,
			companyID, key).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return json.Unmarshal(data, &event)
	})
	return event, found, err
}
