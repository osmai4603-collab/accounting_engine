package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"accounting_engine/pkg/api/v1/events"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"
	"accounting_engine/pkg/sdk"
	accountingv1 "accounting_engine/proto/accounting/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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
	engine sdk.Engine
}

func StartGRPCServer(cfg Config, engine sdk.Engine) error {
	return RunGRPCServer(context.Background(), cfg, engine)
}

func RunGRPCServer(ctx context.Context, cfg Config, engine sdk.Engine) error {
	addr := ":" + cfg.GRPCPort
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	grpcServer := newGRPCServer(engine)
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
	server := grpc.NewServer()
	service := &GRPCServer{engine: engine}
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
	entry, err := s.engine.Ledger().PostEntry(ctx, ledger.JournalEntry{
		ID:          types.NewJournalEntryID(),
		CompanyID:   companyID,
		Description: request.GetDescription(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "post entry: %v", err)
	}
	return &accountingv1.PostEntryResponse{
		EntryId: uuid.UUID(entry.ID).String(),
		Status:  string(entry.Status),
	}, nil
}

func (s *GRPCServer) GetEntry(ctx context.Context, request *accountingv1.GetEntryRequest) (*accountingv1.GetEntryResponse, error) {
	companyID, err := parseCompanyID(request.GetCompanyId())
	if err != nil {
		return nil, err
	}
	entryID, err := parseEntryID(request.GetEntryId())
	if err != nil {
		return nil, err
	}
	entry, err := s.engine.Ledger().GetEntry(ctx, companyID, entryID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get entry: %v", err)
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
	effectiveDate, err := parseDate(request.GetEffectiveDate())
	if err != nil {
		return nil, err
	}
	currency := request.GetCurrency()
	if currency == "" {
		currency = "USD"
	}
	amount, err := types.NewMoneyFromString(request.GetAmount(), currency)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}
	entry, err := s.engine.Events().ProcessEvent(ctx, events.AccountingEvent{
		EventID:        request.GetEventId(),
		EventType:      request.GetEventType(),
		CompanyID:      companyID,
		EffectiveDate:  effectiveDate,
		Amount:         amount,
		IdempotencyKey: request.GetIdempotencyKey(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "process event: %v", err)
	}
	return &accountingv1.ProcessEventResponse{
		EntryId: uuid.UUID(entry.ID).String(),
		Status:  string(entry.Status),
	}, nil
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
