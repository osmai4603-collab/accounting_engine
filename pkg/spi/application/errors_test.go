package application

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorTaxonomyIsInspectable(t *testing.T) {
	cause := fmt.Errorf("driver failure")
	err := NewError(ErrDependencyUnavailable, "storage is unavailable", cause)

	for _, sentinel := range []error{ErrDependencyUnavailable, ErrValidation, ErrNotFound, ErrConflict,
		ErrUnauthorized, ErrForbidden, ErrPeriodClosed, ErrDuplicateRequest, ErrDependencyUnavailable, ErrInternal} {
		if sentinel == ErrDependencyUnavailable {
			if !errors.Is(err, sentinel) {
				t.Fatalf("errors.Is(%v, %v) = false", err, sentinel)
			}
			continue
		}
		if errors.Is(err, sentinel) {
			t.Fatalf("error matched unrelated sentinel %v", sentinel)
		}
	}
	if errors.Is(err, cause) {
		t.Fatal("cause must not be exposed through the error chain")
	}
	var public *Error
	if !errors.As(err, &public) {
		t.Fatal("error is not a *Error")
	}
	if public.PublicMessage() != "storage is unavailable" {
		t.Fatalf("PublicMessage = %q", public.PublicMessage())
	}
	if public.Error() == public.PublicMessage() {
		t.Fatal("server-side Error() should include the cause for logs")
	}
}

func TestErrorWithoutCause(t *testing.T) {
	err := NewError(ErrValidation, "invalid entry", nil)
	if !errors.Is(err, ErrValidation) {
		t.Fatal("errors.Is did not match ErrValidation")
	}
	if err.Error() != "invalid entry" {
		t.Fatalf("Error() = %q", err.Error())
	}
}
