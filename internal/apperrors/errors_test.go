package apperrors_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/apperrors"
)

func TestCodedErrorPreservesCauseAndClassification(t *testing.T) {
	cause := errors.New("connection reset")
	err := apperrors.Wrap(apperrors.CodeInfrastructure, apperrors.Infrastructure, cause)

	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause is not discoverable with errors.Is")
	}
	if got := apperrors.CodeOf(err); got != apperrors.CodeInfrastructure {
		t.Fatalf("CodeOf = %q, want %q", got, apperrors.CodeInfrastructure)
	}
	if got := apperrors.CategoryOf(err); got != apperrors.Infrastructure {
		t.Fatalf("CategoryOf = %q, want %q", got, apperrors.Infrastructure)
	}
	if !apperrors.IsCategory(err, apperrors.Infrastructure) {
		t.Fatal("IsCategory did not match the wrapped category")
	}
}

func TestWrappedCodedErrorSurvivesErrorJoin(t *testing.T) {
	validation := apperrors.New(apperrors.CodeInvalidEntry, apperrors.Validation, "invalid entry")
	joined := errors.Join(errors.New("outer"), validation)

	coded, ok := apperrors.AsCoded(joined)
	if !ok {
		t.Fatal("AsCoded did not find the coded error in a joined error")
	}
	if coded.ErrorCode() != apperrors.CodeInvalidEntry {
		t.Fatalf("code = %q", coded.ErrorCode())
	}
	if apperrors.CategoryOf(joined) != apperrors.Validation {
		t.Fatalf("category = %q, want validation", apperrors.CategoryOf(joined))
	}
}

func TestUnclassifiedErrorsFailClosedAsInfrastructure(t *testing.T) {
	plain := errors.New("unexpected")
	if got := apperrors.CategoryOf(plain); got != apperrors.Infrastructure {
		t.Fatalf("CategoryOf(plain) = %q, want infrastructure", got)
	}
	if got := apperrors.CodeOf(plain); got != apperrors.CodeUnknown {
		t.Fatalf("CodeOf(plain) = %q, want %q", got, apperrors.CodeUnknown)
	}
	if got := apperrors.CategoryOf(nil); got != "" {
		t.Fatalf("CategoryOf(nil) = %q, want empty", got)
	}
	if got := apperrors.CodeOf(nil); got != "" {
		t.Fatalf("CodeOf(nil) = %q, want empty", got)
	}
}

func TestCategoriesAreClosedAndValidated(t *testing.T) {
	for _, category := range []apperrors.Category{
		apperrors.Validation,
		apperrors.Conflict,
		apperrors.Authorization,
		apperrors.Infrastructure,
		apperrors.Invariant,
	} {
		if !category.Valid() {
			t.Errorf("category %q should be valid", category)
		}
	}
	if apperrors.Category("surprise").Valid() {
		t.Fatal("unknown category was reported as valid")
	}
}

func TestCodedErrorFormattingAndNilSafety(t *testing.T) {
	cause := errors.New("boom")
	cases := []struct {
		err  *apperrors.CodedError
		want string
	}{
		{nil, ""},
		{apperrors.New(apperrors.CodeValidation, apperrors.Validation, "bad input"), "VALIDATION_FAILED: bad input"},
		{apperrors.Wrap(apperrors.CodeInfrastructure, apperrors.Infrastructure, cause), "INFRASTRUCTURE_FAILURE: boom"},
		{apperrors.Wrapf(apperrors.CodeConflict, apperrors.Conflict, cause, "entry %s", "abc"), "CONFLICT: entry abc: boom"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
	if _, ok := apperrors.AsCoded(nil); ok {
		t.Fatal("AsCoded(nil) reported ok")
	}
}

func TestCodedErrorInvalidCategoryDefaultsToInfrastructure(t *testing.T) {
	err := &apperrors.CodedError{
		Code:     apperrors.CodeUnknown,
		Category: apperrors.Category("made-up"),
		Message:  fmt.Sprintf("unexpected category"),
	}
	if got := err.ErrorCategory(); got != apperrors.Infrastructure {
		t.Fatalf("invalid category = %q, want infrastructure", got)
	}
}
