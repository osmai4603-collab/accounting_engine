package posting

import (
	"reflect"
	"sync"
	"time"

	"accounting_engine/internal/types"
	"github.com/google/uuid"
)

type MockRepository struct {
	mu        sync.Mutex
	entries   map[uuid.UUID]JournalEntry
	sequences map[uuid.UUID]int64
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		entries:   make(map[uuid.UUID]JournalEntry),
		sequences: make(map[uuid.UUID]int64),
	}
}

func cloneEntry(entry JournalEntry) JournalEntry {
	entry.Lines = append([]JournalEntryLine(nil), entry.Lines...)
	return entry
}

func (r *MockRepository) SaveDraft(entry JournalEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[entry.ID]; ok && existing.Status != EntryDraft {
		return types.ErrCannotModifyPostedMove
	}
	r.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (r *MockRepository) GetEntryByID(id uuid.UUID) (JournalEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[id]
	if !ok {
		return JournalEntry{}, ErrEntryNotFound
	}
	return cloneEntry(entry), nil
}

func (r *MockRepository) PostAtomic(entry JournalEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.entries[entry.ID]
	if ok && existing.Status == EntryPosted && !reflect.DeepEqual(existing.Lines, entry.Lines) {
		return types.ErrCannotModifyPostedMove
	}
	r.entries[entry.ID] = cloneEntry(entry)
	return nil
}

func (r *MockRepository) PostAndSequence(entry JournalEntry) (JournalEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[entry.ID]; ok && existing.Status == EntryPosted &&
		!reflect.DeepEqual(existing.Lines, entry.Lines) {
		return JournalEntry{}, types.ErrCannotModifyPostedMove
	}
	r.sequences[entry.JournalID]++
	entry.SequenceNumber = r.sequences[entry.JournalID]
	r.entries[entry.ID] = cloneEntry(entry)
	return cloneEntry(entry), nil
}

func (r *MockRepository) PostReversalAtomic(original, reversal JournalEntry) (JournalEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	currentOriginal, ok := r.entries[original.ID]
	if !ok {
		return JournalEntry{}, ErrEntryNotFound
	}
	if currentOriginal.Status != EntryPosted || currentOriginal.ReversedEntryID != nil {
		return JournalEntry{}, ErrEntryAlreadyReversed
	}
	currentReversal, ok := r.entries[reversal.ID]
	if !ok || currentReversal.Status != EntryDraft {
		return JournalEntry{}, ErrEntryNotDraft
	}
	r.sequences[reversal.JournalID]++
	reversal.SequenceNumber = r.sequences[reversal.JournalID]
	reversal = cloneEntry(reversal)
	reversal.Status = EntryPosted
	reversalID := reversal.ID
	currentOriginal.ReversedEntryID = &reversalID
	currentOriginal.Status = EntryReversed
	currentOriginal.UpdatedAt = time.Now().UTC()
	r.entries[reversal.ID] = reversal
	r.entries[original.ID] = cloneEntry(currentOriginal)
	return cloneEntry(reversal), nil
}

func (r *MockRepository) GetNextSequence(journalID uuid.UUID) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequences[journalID]++
	return r.sequences[journalID], nil
}

func (r *MockRepository) PostBulk(entries []JournalEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		if existing, ok := r.entries[entry.ID]; ok && existing.Status == EntryPosted &&
			!reflect.DeepEqual(existing.Lines, entry.Lines) {
			return types.ErrCannotModifyPostedMove
		}
	}
	for _, entry := range entries {
		r.entries[entry.ID] = cloneEntry(entry)
	}
	return nil
}
