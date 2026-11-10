package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (d *Driver) SaveOpenLine(ctx context.Context, line subledgers.OpenLine) error {
	return d.SaveOpenLines(ctx, []subledgers.OpenLine{line})
}

func (d *Driver) SaveOpenLineForPostedEntry(ctx context.Context, line subledgers.OpenLine) error {
	if line.ID == uuid.Nil || line.CompanyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, line.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	account, err := scanAccount(tx.QueryRow(ctx,
		accountSelect+` WHERE company_id=$1 AND id=$2 FOR SHARE`, line.CompanyID, line.ControlAccountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return masterdata.ErrAccountNotFound
	}
	if err != nil {
		return err
	}
	var entryData []byte
	err = tx.QueryRow(ctx,
		`SELECT entry FROM account_moves WHERE id=$1 AND company_id=$2 AND status='posted' FOR SHARE`,
		line.JournalEntryID, line.CompanyID).Scan(&entryData)
	if errors.Is(err, pgx.ErrNoRows) {
		return posting.ErrEntryNotPosted
	}
	if err != nil {
		return err
	}
	var entry posting.JournalEntry
	if err := json.Unmarshal(entryData, &entry); err != nil {
		return err
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return err
	}
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	var partner any
	if line.PartnerID != nil {
		partner = *line.PartnerID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data) VALUES($1,$2,$3,$4,$5)`,
		line.ID, line.CompanyID, partner, line.Kind, data); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *Driver) SaveOpenLines(ctx context.Context, lines []subledgers.OpenLine) error {
	if len(lines) == 0 {
		return nil
	}
	companyID := lines[0].CompanyID
	if companyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	for _, line := range lines {
		if line.CompanyID != companyID {
			return subledgers.ErrCompanyMismatch
		}
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, line := range lines {
		if line.ID == uuid.Nil || line.CompanyID == uuid.Nil {
			return subledgers.ErrInvalidEvent
		}
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		var partner any
		if line.PartnerID != nil {
			partner = *line.PartnerID
		}
		result, err := tx.Exec(ctx,
			`INSERT INTO subledger_open_lines(id,company_id,partner_id,kind,line_data)
			 VALUES($1,$2,$3,$4,$5)
			 ON CONFLICT(id) DO UPDATE SET partner_id=EXCLUDED.partner_id,kind=EXCLUDED.kind,line_data=EXCLUDED.line_data
			 WHERE subledger_open_lines.company_id=EXCLUDED.company_id`,
			line.ID, line.CompanyID, partner, line.Kind, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrCompanyMismatch
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) CompareAndSaveOpenLines(ctx context.Context, expected, updated []subledgers.OpenLine) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.OpenLine, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, line := range expected {
		if line.ID == uuid.Nil || line.CompanyID != companyID || updated[i].ID != line.ID ||
			updated[i].CompanyID != line.CompanyID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[line.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[line.ID] = line
		ids = append(ids, line.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT id, company_id, line_data FROM subledger_open_lines
		 WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.OpenLine
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldLine, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldLine.CompanyID || current.CompanyID != oldLine.CompanyID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			rows.Close()
			return subledgers.ErrConcurrentMatch
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	for _, line := range updated {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		var partner any
		if line.PartnerID != nil {
			partner = *line.PartnerID
		}
		result, err := tx.Exec(ctx,
			`UPDATE subledger_open_lines
			 SET partner_id=$3, kind=$4, line_data=$5
			 WHERE id=$1 AND company_id=$2`,
			line.ID, line.CompanyID, partner, line.Kind, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentMatch
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) CompareAndSaveOpenLinesAndDraft(ctx context.Context, expected, updated []subledgers.OpenLine, draft posting.JournalEntry) error {
	if len(expected) == 0 || len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	companyID := expected[0].CompanyID
	if companyID == uuid.Nil || draft.CompanyID != companyID {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, companyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	expectedByID := make(map[uuid.UUID]subledgers.OpenLine, len(expected))
	ids := make([]uuid.UUID, 0, len(expected))
	for i, line := range expected {
		if line.ID == uuid.Nil || line.CompanyID != companyID || updated[i].ID != line.ID ||
			updated[i].CompanyID != line.CompanyID || draft.CompanyID != line.CompanyID {
			return subledgers.ErrInvalidOpenLine
		}
		if _, exists := expectedByID[line.ID]; exists {
			return subledgers.ErrInvalidOpenLine
		}
		expectedByID[line.ID] = line
		ids = append(ids, line.ID)
	}
	rows, err := tx.Query(ctx,
		`SELECT id, company_id, line_data FROM subledger_open_lines
		 WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var id, companyID uuid.UUID
		var data []byte
		if err := rows.Scan(&id, &companyID, &data); err != nil {
			rows.Close()
			return err
		}
		var current subledgers.OpenLine
		if err := json.Unmarshal(data, &current); err != nil {
			rows.Close()
			return err
		}
		oldLine, ok := expectedByID[id]
		if !ok {
			rows.Close()
			return subledgers.ErrInvalidOpenLine
		}
		if companyID != oldLine.CompanyID || current.CompanyID != oldLine.CompanyID {
			rows.Close()
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			rows.Close()
			return subledgers.ErrConcurrentMatch
		}
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found != len(expected) {
		return subledgers.ErrNotFound
	}
	if err := d.saveEntryTx(ctx, tx, draft); err != nil {
		return err
	}
	for _, line := range updated {
		data, err := json.Marshal(line)
		if err != nil {
			return err
		}
		var partner any
		if line.PartnerID != nil {
			partner = *line.PartnerID
		}
		result, err := tx.Exec(ctx,
			`UPDATE subledger_open_lines
			 SET partner_id=$3, kind=$4, line_data=$5
			 WHERE id=$1 AND company_id=$2`,
			line.ID, line.CompanyID, partner, line.Kind, data)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return subledgers.ErrConcurrentMatch
		}
	}
	return tx.Commit(ctx)
}

func (d *Driver) GetOpenLine(ctx context.Context, companyID, id uuid.UUID) (subledgers.OpenLine, error) {
	var line subledgers.OpenLine
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var data []byte
		err := tx.QueryRow(ctx,
			`SELECT line_data FROM subledger_open_lines WHERE company_id=$1 AND id=$2`,
			companyID, id).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			return subledgers.ErrNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &line)
	})
	return line, err
}

func (d *Driver) ConfirmOpenLineWriteOff(ctx context.Context, expected, updated subledgers.OpenLine, postedEntry posting.JournalEntry) error {
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || postedEntry.CompanyID != expected.CompanyID ||
		postedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidOpenLine
	}
	tx, err := d.beginCompanyTx(ctx, expected.CompanyID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentData []byte
	err = tx.QueryRow(ctx,
		`SELECT line_data FROM subledger_open_lines WHERE id=$1 AND company_id=$2 FOR UPDATE`,
		expected.ID, expected.CompanyID).Scan(&currentData)
	if errors.Is(err, pgx.ErrNoRows) {
		return subledgers.ErrNotFound
	}
	if err != nil {
		return err
	}
	var current subledgers.OpenLine
	if err := json.Unmarshal(currentData, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentMatch
	}
	var entryData []byte
	err = tx.QueryRow(ctx,
		`SELECT entry FROM account_moves WHERE id=$1 AND company_id=$2 AND status='posted' FOR SHARE`,
		postedEntry.ID, postedEntry.CompanyID).Scan(&entryData)
	if errors.Is(err, pgx.ErrNoRows) {
		return posting.ErrEntryNotPosted
	}
	if err != nil {
		return err
	}
	var actualEntry posting.JournalEntry
	if err := json.Unmarshal(entryData, &actualEntry); err != nil {
		return err
	}
	if !subledgers.FixedAssetEntryMatches(actualEntry, postedEntry) {
		return posting.ErrEntryNotPosted
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx,
		`UPDATE subledger_open_lines SET line_data=$3 WHERE id=$1 AND company_id=$2`,
		updated.ID, updated.CompanyID, data)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return subledgers.ErrConcurrentMatch
	}
	return tx.Commit(ctx)
}

func (d *Driver) ListOpenLines(ctx context.Context, partnerID string) ([]subledgers.OpenLine, error) {
	return nil, subledgers.ErrCompanyScopedOpenLinesRequired
}

func (d *Driver) ListOpenLinesForCompany(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	query := `SELECT line_data FROM subledger_open_lines WHERE company_id=$1`
	args := []any{companyID}
	if partnerID != "" {
		query += ` AND partner_id=$2`
		args = append(args, partnerID)
	}
	result := make([]subledgers.OpenLine, 0)
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var line subledgers.OpenLine
			if err := json.Unmarshal(data, &line); err != nil {
				return err
			}
			result = append(result, line)
		}
		return rows.Err()
	})
	return result, err
}
func (d *Driver) ListOpenLinesForCompanyContext(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	return d.ListOpenLinesForCompany(ctx, companyID, partnerID)
}
