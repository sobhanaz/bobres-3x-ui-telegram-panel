package provisionerclient

import (
	"context"
	"errors"
	"testing"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"google.golang.org/grpc"
)

type logsStub struct {
	provisionerv1.ProvisionerServiceClient
	resp *provisionerv1.RecentLogsResponse
	err  error
}

func (s logsStub) RecentLogs(context.Context, *provisionerv1.RecentLogsRequest, ...grpc.CallOption) (*provisionerv1.RecentLogsResponse, error) {
	return s.resp, s.err
}

func TestRecentLogs(t *testing.T) {
	stub := logsStub{resp: &provisionerv1.RecentLogsResponse{Lines: []*provisionerv1.LogLine{{Time: 1760000000, Level: "WARN", Msg: "m", Attrs: "{}"}}}}
	got, err := New(stub).RecentLogs(context.Background())
	if err != nil || len(got) != 1 || got[0] != (logging.Line{Time: 1760000000, Level: "WARN", Msg: "m", Attrs: "{}"}) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := New(logsStub{err: errors.New("down")}).RecentLogs(context.Background()); err == nil {
		t.Fatal("an error was lost")
	}
}
