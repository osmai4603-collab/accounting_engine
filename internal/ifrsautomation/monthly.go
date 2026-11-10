package ifrsautomation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"accounting_engine/internal/posting"
	"github.com/google/uuid"
)

var (
	ErrInvalidPeriod        = errors.New("invalid IFRS automation period")
	ErrInvalidJob           = errors.New("invalid IFRS automation job")
	ErrEntryOutsidePeriod   = errors.New("generated journal entry is outside the automation period")
	ErrEntryContentChanged  = errors.New("existing automated journal entry differs from generated content")
	ErrEntryAlreadyReversed = errors.New("automated journal entry has already been reversed")
	ErrDependenciesRequired = errors.New("IFRS automation posting dependencies are required")
)

type Period struct {
	Start time.Time
	End   time.Time
}

func (p Period) IsFullMonth() bool {
	if p.Validate() != nil {
		return false
	}
	start, end := dateOnlyUTC(p.Start), dateOnlyUTC(p.End)
	return start.Day() == 1 && end.Equal(start.AddDate(0, 1, -1))
}

func (p Period) Validate() error {
	if p.Start.IsZero() || p.End.IsZero() || p.Start.After(p.End) {
		return ErrInvalidPeriod
	}
	return nil
}

func Month(year int, month time.Month) (Period, error) {
	if year < 1 || month < time.January || month > time.December {
		return Period{}, ErrInvalidPeriod
	}
	start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	return Period{Start: start, End: start.AddDate(0, 1, -1)}, nil
}

type Job interface {
	Key() string
	Generate(context.Context, Period) ([]posting.JournalEntry, error)
}

type CompletionHook interface {
	Complete(context.Context, Period) error
}

type JobFunc struct {
	ID      string
	Execute func(context.Context, Period) ([]posting.JournalEntry, error)
}

func (j JobFunc) Key() string { return j.ID }

func (j JobFunc) Generate(ctx context.Context, period Period) ([]posting.JournalEntry, error) {
	if j.Execute == nil {
		return nil, ErrInvalidJob
	}
	return j.Execute(ctx, period)
}

type Poster interface {
	CreateDraftContext(context.Context, posting.JournalEntry) error
	PostContext(context.Context, uuid.UUID, uuid.UUID, string) error
}

type EntryReader interface {
	GetEntryByIDForCompany(context.Context, uuid.UUID, uuid.UUID) (posting.JournalEntry, error)
}

type Runner struct {
	mu       sync.Mutex
	poster   Poster
	reader   EntryReader
	posterID string
}

type RunResult struct {
	Period         Period
	PostedEntryIDs []uuid.UUID
	AlreadyPosted  []uuid.UUID
}

func NewRunner(poster Poster, reader EntryReader, posterID string) (*Runner, error) {
	if poster == nil || reader == nil || strings.TrimSpace(posterID) == "" {
		return nil, ErrDependenciesRequired
	}
	return &Runner{poster: poster, reader: reader, posterID: posterID}, nil
}

func (r *Runner) RunMonthly(ctx context.Context, period Period, jobs ...Job) (RunResult, error) {
	if err := period.Validate(); err != nil {
		return RunResult{}, err
	}
	if !period.IsFullMonth() {
		return RunResult{}, ErrInvalidPeriod
	}
	if r == nil || r.poster == nil || r.reader == nil || strings.TrimSpace(r.posterID) == "" {
		return RunResult{}, ErrDependenciesRequired
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	orderedJobs := append([]Job(nil), jobs...)
	seenJobs := make(map[string]struct{}, len(orderedJobs))
	for _, job := range orderedJobs {
		if job == nil || strings.TrimSpace(job.Key()) == "" {
			return RunResult{}, ErrInvalidJob
		}
		if _, exists := seenJobs[job.Key()]; exists {
			return RunResult{}, fmt.Errorf("%w: duplicate job key %q", ErrInvalidJob, job.Key())
		}
		seenJobs[job.Key()] = struct{}{}
	}
	sort.Slice(orderedJobs, func(i, j int) bool {
		return orderedJobs[i].Key() < orderedJobs[j].Key()
	})
	result := RunResult{Period: period}
	for _, job := range orderedJobs {
		entries, err := job.Generate(ctx, period)
		if err != nil {
			return result, fmt.Errorf("generate monthly entries for %s: %w", job.Key(), err)
		}
		entries, err = prepareEntries(job.Key(), period, entries)
		if err != nil {
			return result, err
		}
		for _, candidate := range entries {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			alreadyPosted, err := r.postOrSkip(ctx, &candidate)
			if err != nil {
				return result, fmt.Errorf("post monthly entry %s for %s: %w", candidate.ID, job.Key(), err)
			}
			if alreadyPosted {
				result.AlreadyPosted = append(result.AlreadyPosted, candidate.ID)
			} else {
				result.PostedEntryIDs = append(result.PostedEntryIDs, candidate.ID)
			}
		}
		if completion, ok := job.(CompletionHook); ok {
			if err := completion.Complete(ctx, period); err != nil {
				return result, fmt.Errorf("complete monthly job %s: %w", job.Key(), err)
			}
		}
	}
	return result, nil
}

func (r *Runner) postOrSkip(ctx context.Context, candidate *posting.JournalEntry) (bool, error) {
	saved, err := r.reader.GetEntryByIDForCompany(ctx, candidate.CompanyID, candidate.ID)
	switch {
	case err == nil:
		if !posting.DraftContentMatches(saved, *candidate) {
			return false, ErrEntryContentChanged
		}
		switch saved.Status {
		case posting.EntryPosted:
			candidate.Status = posting.EntryPosted
			return true, nil
		case posting.EntryDraft:
			if err := r.poster.PostContext(ctx, candidate.CompanyID, candidate.ID, r.posterID); err != nil {
				return false, err
			}
			candidate.Status = posting.EntryPosted
			return false, nil
		case posting.EntryReversed:
			return false, ErrEntryAlreadyReversed
		default:
			return false, posting.ErrInvalidEntryState
		}
	case errors.Is(err, posting.ErrEntryNotFound):
		if err := r.poster.CreateDraftContext(ctx, *candidate); err != nil {
			return false, err
		}
		if err := r.poster.PostContext(ctx, candidate.CompanyID, candidate.ID, r.posterID); err != nil {
			return false, err
		}
		candidate.Status = posting.EntryPosted
		return false, nil
	default:
		return false, err
	}
}

func prepareEntries(jobKey string, period Period, entries []posting.JournalEntry) ([]posting.JournalEntry, error) {
	if entries == nil {
		return nil, nil
	}
	result := append([]posting.JournalEntry(nil), entries...)
	for i := range result {
		entry := &result[i]
		if entry.CompanyID == uuid.Nil || entry.JournalID == uuid.Nil {
			return nil, fmt.Errorf("%w: company and journal are required", ErrInvalidJob)
		}
		if strings.TrimSpace(entry.Reference) == "" && strings.TrimSpace(entry.DocumentReference) == "" {
			return nil, fmt.Errorf("%w: each automated entry needs a stable reference", ErrInvalidJob)
		}
		date := entry.EntryDate
		if date.IsZero() {
			date = entry.Date
		}
		if date.IsZero() || date.Before(period.Start) || date.After(period.End) {
			return nil, fmt.Errorf("%w: %s", ErrEntryOutsidePeriod, entry.ID)
		}
		entry.EntryDate, entry.Date = date.UTC(), date.UTC()
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if !a.EntryDate.Equal(b.EntryDate) {
			return a.EntryDate.Before(b.EntryDate)
		}
		if a.CompanyID != b.CompanyID {
			return a.CompanyID.String() < b.CompanyID.String()
		}
		if a.JournalID != b.JournalID {
			return a.JournalID.String() < b.JournalID.String()
		}
		return entryFingerprint(a) < entryFingerprint(b)
	})
	seenIDs := make(map[uuid.UUID]struct{}, len(result))
	for i := range result {
		entry := &result[i]
		entry.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(jobKey+":"+entryIdentity(*entry)))
		if _, exists := seenIDs[entry.ID]; exists {
			return nil, fmt.Errorf("%w: entries need unique stable references", ErrInvalidJob)
		}
		seenIDs[entry.ID] = struct{}{}
		for lineIndex := range entry.Lines {
			entry.Lines[lineIndex].ID = uuid.NewSHA1(entry.ID, []byte(fmt.Sprintf("line:%d", lineIndex)))
			entry.Lines[lineIndex].CompanyID = entry.CompanyID
		}
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("%w: generated entry validation: %v", ErrInvalidJob, err)
		}
	}
	return result, nil
}

func entryFingerprint(entry posting.JournalEntry) string {
	var parts []string
	parts = append(parts, entry.CompanyID.String(), entry.JournalID.String(),
		entry.EntryDate.UTC().Format(time.RFC3339Nano), entry.Reference, entry.DocumentReference,
		string(entry.Classification), entry.CreatedBy)
	for _, line := range entry.Lines {
		parts = append(parts, line.AccountID.String(), line.Debit.String(), line.Credit.String(),
			line.TransactionAmount.String(), line.FunctionalDebit.String(), line.FunctionalCredit.String(),
			uuidString(line.PartnerID), uuidString(line.CostCenterID), uuidString(line.ProjectID),
			uuidString(line.SegmentID), uuidString(line.BusinessUnitID))
	}
	return strings.Join(parts, "|")
}

func entryIdentity(entry posting.JournalEntry) string {
	return strings.Join([]string{
		entry.CompanyID.String(), entry.JournalID.String(), entry.EntryDate.UTC().Format(time.RFC3339Nano),
		entry.Reference, entry.DocumentReference, string(entry.Classification), entry.CreatedBy,
	}, "|")
}

func uuidString(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}
