package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/serviceauth"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/events"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	accountingv1 "github.com/osmai4603-collab/accounting_engine/proto/accounting/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

type GRPCServer struct {
	accountingv1.UnimplementedAccountingServiceServer
	accountingv1.UnimplementedEventServiceServer
	accountingv1.UnimplementedReportingServiceServer
	engine           sdk.Engine
	requirePrincipal bool
}

func StartGRPCServer(cfg Config, engine sdk.Engine) error {
	return RunGRPCServer(context.Background(), cfg, engine)
}

func RunGRPCServer(ctx context.Context, cfg Config, engine sdk.Engine) error {
	verifier, err := newServiceVerifier(cfg)
	if err != nil {
		return fmt.Errorf("service authentication configuration: %w", err)
	}
	addr := ":" + cfg.GRPCPort
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	grpcServer := newGRPCServerWithVerifier(engine, verifier)
	log.Printf("Starting Standalone Accounting Service gRPC Daemon on %s", addr)

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- grpcServer.Serve(listener)
	}()

	select {
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	timer := time.NewTimer(serverShutdownTimeout)
	defer timer.Stop()
	select {
	case <-stopped:
		return nil
	case <-timer.C:
		grpcServer.Stop()
		<-stopped
		return fmt.Errorf("graceful gRPC shutdown exceeded %s", serverShutdownTimeout)
	}
}

func newGRPCServer(engine sdk.Engine) *grpc.Server {
	return newGRPCServerWithVerifier(engine, nil)
}

// newGRPCServerWithVerifier installs authentication interceptors when verifier
// is non-nil. Reflection streams are protected by the same stream interceptor.
func newGRPCServerWithVerifier(engine sdk.Engine, verifier serviceauth.Verifier) *grpc.Server {
	options := make([]grpc.ServerOption, 0, 2)
	requirePrincipal := verifier != nil
	if verifier != nil {
		boundary := &grpcAuthBoundary{verifier: verifier}
		options = append(options,
			grpc.UnaryInterceptor(boundary.unaryInterceptor),
			grpc.StreamInterceptor(boundary.streamInterceptor),
		)
	}
	server := grpc.NewServer(options...)
	service := &GRPCServer{engine: engine, requirePrincipal: requirePrincipal}
	accountingv1.RegisterAccountingServiceServer(server, service)
	accountingv1.RegisterReportingServiceServer(server, service)
	accountingv1.RegisterEventServiceServer(server, service)

	descriptors := new(protoregistry.Files)
	for _, file := range []protoreflect.FileDescriptor{
		accountingv1.File_proto_accounting_v1_ledger_proto,
		accountingv1.File_proto_accounting_v1_reporting_proto,
		accountingv1.File_proto_accounting_v1_events_proto,
	} {
		if err := descriptors.RegisterFile(file); err != nil {
			panic(err)
		}
	}
	reflectionv1.RegisterServerReflectionServer(server, reflection.NewServerV1(reflection.ServerOptions{
		Services:           server,
		DescriptorResolver: descriptors,
	}))
	return server
}

func (s *GRPCServer) PostEntry(ctx context.Context, request *accountingv1.PostEntryRequest) (*accountingv1.PostEntryResponse, error) {
	companyID, err := parseCompanyID(request.GetCompanyId())
	if err != nil {
		return nil, err
	}
	if err := s.authorizeCompany(ctx, companyID); err != nil {
		return nil, err
	}
	return nil, status.Error(codes.InvalidArgument, "PostEntry requires a complete journal entry; this request only supplies company_id and description")
}

func (s *GRPCServer) GetEntry(ctx context.Context, request *accountingv1.GetEntryRequest) (*accountingv1.GetEntryResponse, error) {
	companyID, err := parseCompanyID(request.GetCompanyId())
	if err != nil {
		return nil, err
	}
	if err := s.authorizeCompany(ctx, companyID); err != nil {
		return nil, err
	}
	entryID, err := parseEntryID(request.GetEntryId())
	if err != nil {
		return nil, err
	}
	entry, err := s.engine.Ledger().GetEntry(ctx, companyID, entryID)
	if err != nil {
		return nil, grpcApplicationError(err)
	}
	return &accountingv1.GetEntryResponse{
		EntryId: uuid.UUID(entry.ID).String(),
		Status:  string(entry.Status),
	}, nil
}

func (s *GRPCServer) GetTrialBalance(ctx context.Context, request *accountingv1.GetTrialBalanceRequest) (*accountingv1.GetTrialBalanceResponse, error) {
	companyID, err := parseCompanyID(request.GetCompanyId())
	if err != nil {
		return nil, err
	}
	if err := s.authorizeCompany(ctx, companyID); err != nil {
		return nil, err
	}
	asOf, err := parseDate(request.GetAsOfDate())
	if err != nil {
		return nil, err
	}
	currency := request.GetCurrency()
	if currency == "" {
		currency = "USD"
	}
	trialBalance, err := s.engine.Reporting().GetTrialBalance(ctx, companyID, asOf, currency)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get trial balance: %v", err)
	}
	return &accountingv1.GetTrialBalanceResponse{
		CompanyId:  uuid.UUID(trialBalance.CompanyID).String(),
		IsBalanced: trialBalance.IsBalanced,
	}, nil
}

func (s *GRPCServer) GetBalanceSheet(ctx context.Context, request *accountingv1.GetBalanceSheetRequest) (*accountingv1.GetBalanceSheetResponse, error) {
	companyID, err := parseCompanyID(request.GetCompanyId())
	if err != nil {
		return nil, err
	}
	if err := s.authorizeCompany(ctx, companyID); err != nil {
		return nil, err
	}
	asOf, err := parseDate(request.GetAsOfDate())
	if err != nil {
		return nil, err
	}
	currency := request.GetCurrency()
	if currency == "" {
		currency = "USD"
	}
	report, err := s.engine.Reporting().GetBalanceSheet(ctx, companyID, asOf, currency)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get balance sheet: %v", err)
	}
	return &accountingv1.GetBalanceSheetResponse{
		CompanyId: uuid.UUID(report.CompanyID).String(),
		Success:   true,
	}, nil
}

func (s *GRPCServer) GetIncomeStatement(ctx context.Context, request *accountingv1.GetIncomeStatementRequest) (*accountingv1.GetIncomeStatementResponse, error) {
	companyID, err := parseCompanyID(request.GetCompanyId())
	if err != nil {
		return nil, err
	}
	if err := s.authorizeCompany(ctx, companyID); err != nil {
		return nil, err
	}
	start, err := parseDate(request.GetStartDate())
	if err != nil {
		return nil, err
	}
	end, err := parseDate(request.GetEndDate())
	if err != nil {
		return nil, err
	}
	currency := request.GetCurrency()
	if currency == "" {
		currency = "USD"
	}
	report, err := s.engine.Reporting().GetIncomeStatement(ctx, companyID, start, end, currency)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get income statement: %v", err)
	}
	return &accountingv1.GetIncomeStatementResponse{
		CompanyId: uuid.UUID(report.CompanyID).String(),
		Success:   true,
	}, nil
}

func (s *GRPCServer) ProcessEvent(ctx context.Context, request *accountingv1.ProcessEventRequest) (*accountingv1.ProcessEventResponse, error) {
	companyID, err := parseCompanyID(request.GetCompanyId())
	if err != nil {
		return nil, err
	}
	if err := s.authorizeCompany(ctx, companyID); err != nil {
		return nil, err
	}
	if request.GetEffectiveDate() == "" {
		return nil, status.Error(codes.InvalidArgument, "effective_date is required")
	}
	effectiveDate, err := parseDate(request.GetEffectiveDate())
	if err != nil {
		return nil, err
	}
	currency := request.GetCurrency()
	if currency == "" {
		return nil, status.Error(codes.InvalidArgument, "currency is required")
	}
	amount, err := types.NewMoneyFromString(request.GetAmount(), currency)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}
	requestMetadata := grpcRequestMetadata(ctx, "accounting-service")
	requestMetadata.IdempotencyKey = firstNonEmpty(request.GetIdempotencyKey(), requestMetadata.IdempotencyKey)
	ctx = publicapplication.WithRequestMetadata(ctx, requestMetadata)
	entry, err := s.engine.Events().ProcessEvent(ctx, events.AccountingEvent{
		EventID:        request.GetEventId(),
		EventType:      request.GetEventType(),
		CompanyID:      companyID,
		EffectiveDate:  effectiveDate,
		Amount:         amount,
		IdempotencyKey: requestMetadata.IdempotencyKey,
	})
	if err != nil {
		return nil, grpcApplicationError(err)
	}
	return &accountingv1.ProcessEventResponse{
		EntryId: uuid.UUID(entry.ID).String(),
		Status:  string(entry.Status),
	}, nil
}

func (s *GRPCServer) authorizeCompany(ctx context.Context, companyID types.CompanyID) error {
	principal, ok := publicapplication.PrincipalFromContext(ctx)
	if !ok {
		if s.requirePrincipal {
			return status.Error(codes.Unauthenticated, "authentication required")
		}
		return nil
	}
	for _, allowed := range principal.CompanyIDs {
		if allowed == companyID {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied, "company access denied")
}

func grpcRequestMetadata(ctx context.Context, fallbackSource string) publicapplication.RequestMetadata {
	incoming, _ := metadata.FromIncomingContext(ctx)
	first := func(key string) string {
		values := incoming.Get(key)
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	// Prefer metadata already injected by the authentication boundary and fill
	// only missing fields so the boundary stays authoritative.
	requestMetadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	if requestMetadata.CorrelationID == "" {
		requestMetadata.CorrelationID = first("x-correlation-id")
	}
	if requestMetadata.IdempotencyKey == "" {
		requestMetadata.IdempotencyKey = first("idempotency-key")
	}
	if requestMetadata.SourceSystem == "" {
		requestMetadata.SourceSystem = headerOrDefault(first("x-source-system"), fallbackSource)
	}
	return requestMetadata
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func grpcApplicationError(err error) error {
	message := "internal error"
	var classified interface {
		PublicMessage() string
	}
	if !errors.As(err, &classified) {
		return status.Error(codes.Internal, message)
	}
	message = classified.PublicMessage()
	switch {
	case errors.Is(err, publicapplication.ErrValidation):
		return status.Error(codes.InvalidArgument, message)
	case errors.Is(err, publicapplication.ErrUnauthorized):
		return status.Error(codes.Unauthenticated, message)
	case errors.Is(err, publicapplication.ErrForbidden):
		return status.Error(codes.PermissionDenied, message)
	case errors.Is(err, publicapplication.ErrNotFound):
		return status.Error(codes.NotFound, message)
	case errors.Is(err, publicapplication.ErrConflict):
		return status.Error(codes.Aborted, message)
	case errors.Is(err, publicapplication.ErrDuplicateRequest):
		return status.Error(codes.AlreadyExists, message)
	case errors.Is(err, publicapplication.ErrDependencyUnavailable):
		return status.Error(codes.Unavailable, message)
	default:
		return status.Error(codes.Internal, message)
	}
}

func parseCompanyID(value string) (types.CompanyID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return types.CompanyID{}, status.Error(codes.InvalidArgument, "invalid company_id")
	}
	return types.CompanyID(id), nil
}

func parseEntryID(value string) (types.JournalEntryID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return types.JournalEntryID{}, status.Error(codes.InvalidArgument, "invalid entry_id")
	}
	return types.JournalEntryID(id), nil
}

func parseDate(value string) (time.Time, error) {
	if value == "" {
		return time.Now().UTC(), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, status.Error(codes.InvalidArgument, "invalid date")
}
