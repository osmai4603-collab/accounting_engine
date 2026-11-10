package memory

import (
	"context"
	"reflect"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"

	"github.com/google/uuid"
)

func (d *Driver) SaveOpenLine(ctx context.Context, line subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	d.lines[line.ID] = line
	return nil
}

func (d *Driver) SaveOpenLineForPostedEntry(ctx context.Context, line subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	account, accountExists := d.accounts[line.ControlAccountID]
	entry, entryExists := d.entries[line.JournalEntryID]
	if !accountExists {
		return masterdata.ErrAccountNotFound
	}
	if !entryExists {
		return posting.ErrEntryNotFound
	}
	if err := subledgers.ValidatePostedOpenLine(line, account, entry); err != nil {
		return err
	}
	if existing, exists := d.lines[line.ID]; exists && existing.CompanyID != line.CompanyID {
		return subledgers.ErrCompanyMismatch
	}
	if _, exists := d.lines[line.ID]; exists {
		return subledgers.ErrAlreadyExists
	}
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
	d.lines[line.ID] = line
	return nil
}

func (d *Driver) SaveOpenLines(ctx context.Context, lines []subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, line := range lines {
		if existing, ok := d.lines[line.ID]; ok && existing.CompanyID != line.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
	}
	for _, line := range lines {
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		d.lines[line.ID] = line
	}
	return nil
}

func (d *Driver) CompareAndSaveOpenLines(ctx context.Context, expected, updated []subledgers.OpenLine) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldLine := range expected {
		current, ok := d.lines[oldLine.ID]
		if !ok {
			return subledgers.ErrNotFound
		}
		if current.CompanyID != oldLine.CompanyID || updated[i].ID != oldLine.ID ||
			updated[i].CompanyID != oldLine.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			return subledgers.ErrConcurrentMatch
		}
	}
	for _, line := range updated {
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		d.lines[line.ID] = line
	}
	return nil
}

func (d *Driver) CompareAndSaveOpenLinesAndDraft(ctx context.Context, expected, updated []subledgers.OpenLine, draft posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if draft.ID == uuid.Nil || draft.Status != posting.EntryDraft {
		return posting.ErrInvalidEntry
	}
	if err := draft.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(expected) != len(updated) {
		return subledgers.ErrInvalidOpenLine
	}
	for i, oldLine := range expected {
		current, ok := d.lines[oldLine.ID]
		if !ok {
			return subledgers.ErrNotFound
		}
		if current.CompanyID != oldLine.CompanyID || updated[i].ID != oldLine.ID ||
			updated[i].CompanyID != oldLine.CompanyID || draft.CompanyID != oldLine.CompanyID {
			return subledgers.ErrCompanyMismatch
		}
		if !reflect.DeepEqual(current, oldLine) {
			return subledgers.ErrConcurrentMatch
		}
	}
	if current, exists := d.entries[draft.ID]; exists && current.Status != posting.EntryDraft {
		return posting.ErrEntryNotDraft
	}
	for i := range draft.Lines {
		if draft.Lines[i].CompanyID != uuid.Nil && draft.Lines[i].CompanyID != draft.CompanyID {
			return posting.ErrInvalidEntry
		}
		account, ok := d.accounts[draft.Lines[i].AccountID]
		if !ok || account.CompanyID != draft.CompanyID {
			return masterdata.ErrAccountNotFound
		}
		draft.Lines[i].CompanyID = draft.CompanyID
	}
	for _, line := range updated {
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		d.lines[line.ID] = line
	}
	d.entries[draft.ID] = cloneEntry(draft)
	return nil
}

func (d *Driver) GetOpenLine(ctx context.Context, companyID, id uuid.UUID) (subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return subledgers.OpenLine{}, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	line, ok := d.lines[id]
	if !ok || line.CompanyID != companyID {
		return subledgers.OpenLine{}, subledgers.ErrNotFound
	}
	line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
	line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
	return line, nil
}

func (d *Driver) ConfirmOpenLineWriteOff(ctx context.Context, expected, updated subledgers.OpenLine, postedEntry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.ID == uuid.Nil || expected.CompanyID == uuid.Nil || updated.ID != expected.ID ||
		updated.CompanyID != expected.CompanyID || postedEntry.CompanyID != expected.CompanyID ||
		postedEntry.Status != posting.EntryPosted {
		return subledgers.ErrInvalidOpenLine
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.lines[expected.ID]
	if !ok {
		return subledgers.ErrNotFound
	}
	if !reflect.DeepEqual(current, expected) {
		return subledgers.ErrConcurrentMatch
	}
	actual, ok := d.entries[postedEntry.ID]
	if !ok {
		return posting.ErrEntryNotFound
	}
	if !subledgers.FixedAssetEntryMatches(actual, postedEntry) {
		return posting.ErrEntryNotPosted
	}
	updated.MatchIDs = append([]uuid.UUID(nil), updated.MatchIDs...)
	updated.Matches = append([]subledgers.OpenLineMatch(nil), updated.Matches...)
	d.lines[updated.ID] = updated
	return nil
}

func (d *Driver) ListOpenLines(ctx context.Context, partnerID string) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]subledgers.OpenLine, 0)
	for _, line := range d.lines {
		if partnerID == "" || (line.PartnerID != nil && line.PartnerID.String() == partnerID) {
			line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
			line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
			result = append(result, line)
		}
	}
	return result, nil
}

func (d *Driver) ListOpenLinesForCompany(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]subledgers.OpenLine, 0)
	for _, line := range d.lines {
		if line.CompanyID != companyID || (partnerID != "" && (line.PartnerID == nil || line.PartnerID.String() != partnerID)) {
			continue
		}
		line.MatchIDs = append([]uuid.UUID(nil), line.MatchIDs...)
		line.Matches = append([]subledgers.OpenLineMatch(nil), line.Matches...)
		result = append(result, line)
	}
	return result, nil
}
func (d *Driver) ListOpenLinesForCompanyContext(ctx context.Context, companyID uuid.UUID, partnerID string) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.ListOpenLinesForCompany(ctx, companyID, partnerID)
}
