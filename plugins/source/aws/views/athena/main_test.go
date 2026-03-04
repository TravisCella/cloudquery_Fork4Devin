package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/athena/types"
)

// mockAthenaClient implements AthenaAPI with configurable return values/errors for each method.
type mockAthenaClient struct {
	startQueryFn   func(ctx context.Context, params *athena.StartQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.StartQueryExecutionOutput, error)
	getQueryExecFn func(ctx context.Context, params *athena.GetQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.GetQueryExecutionOutput, error)
	getResultsFn   func(ctx context.Context, params *athena.GetQueryResultsInput, optFns ...func(*athena.Options)) (*athena.GetQueryResultsOutput, error)
}

func (m *mockAthenaClient) StartQueryExecution(ctx context.Context, params *athena.StartQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.StartQueryExecutionOutput, error) {
	return m.startQueryFn(ctx, params, optFns...)
}

func (m *mockAthenaClient) GetQueryExecution(ctx context.Context, params *athena.GetQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.GetQueryExecutionOutput, error) {
	return m.getQueryExecFn(ctx, params, optFns...)
}

func (m *mockAthenaClient) GetQueryResults(ctx context.Context, params *athena.GetQueryResultsInput, optFns ...func(*athena.Options)) (*athena.GetQueryResultsOutput, error) {
	return m.getResultsFn(ctx, params, optFns...)
}

// Helper to create a succeeded GetQueryExecution response.
func succeededExecOutput() *athena.GetQueryExecutionOutput {
	return &athena.GetQueryExecutionOutput{
		QueryExecution: &types.QueryExecution{
			Status: &types.QueryExecutionStatus{
				State: types.QueryExecutionStateSucceeded,
			},
		},
	}
}

// Helper to build a GetQueryResults response with a header row and data rows.
// Each data row is a slice of 4 strings: [table_name, has_region, has_tags, tags_data_type].
func buildQueryResultsOutput(rows [][]string) *athena.GetQueryResultsOutput {
	headerRow := types.Row{
		Data: []types.Datum{
			{VarCharValue: aws.String("table_name")},
			{VarCharValue: aws.String("has_region")},
			{VarCharValue: aws.String("has_tags")},
			{VarCharValue: aws.String("tags_data_type")},
		},
	}
	resultRows := []types.Row{headerRow}
	for _, r := range rows {
		resultRows = append(resultRows, types.Row{
			Data: []types.Datum{
				{VarCharValue: aws.String(r[0])},
				{VarCharValue: aws.String(r[1])},
				{VarCharValue: aws.String(r[2])},
				{VarCharValue: aws.String(r[3])},
			},
		})
	}
	return &athena.GetQueryResultsOutput{
		ResultSet: &types.ResultSet{
			Rows: resultRows,
		},
	}
}

func TestWaitForResults(t *testing.T) {
	tests := []struct {
		name        string
		execOutputs []*athena.GetQueryExecutionOutput
		execErr     error
		wantErr     bool
		errContains string
	}{
		{
			name: "succeeds on first poll",
			execOutputs: []*athena.GetQueryExecutionOutput{
				succeededExecOutput(),
			},
			wantErr: false,
		},
		{
			name: "query failed",
			execOutputs: []*athena.GetQueryExecutionOutput{
				{
					QueryExecution: &types.QueryExecution{
						Status: &types.QueryExecutionStatus{
							State: types.QueryExecutionStateFailed,
						},
					},
				},
			},
			wantErr:     true,
			errContains: "query failed",
		},
		{
			name: "query cancelled",
			execOutputs: []*athena.GetQueryExecutionOutput{
				{
					QueryExecution: &types.QueryExecution{
						Status: &types.QueryExecutionStatus{
							State: types.QueryExecutionStateCancelled,
						},
					},
				},
			},
			wantErr:     true,
			errContains: "query cancelled",
		},
		{
			name:        "GetQueryExecution returns SDK error",
			execErr:     errors.New("SDK error: access denied"),
			wantErr:     true,
			errContains: "SDK error: access denied",
		},
		{
			name: "succeeds after multiple polls",
			execOutputs: []*athena.GetQueryExecutionOutput{
				{
					QueryExecution: &types.QueryExecution{
						Status: &types.QueryExecutionStatus{
							State: types.QueryExecutionStateRunning,
						},
					},
				},
				{
					QueryExecution: &types.QueryExecution{
						Status: &types.QueryExecutionStatus{
							State: types.QueryExecutionStateRunning,
						},
					},
				},
				succeededExecOutput(),
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callCount := 0
			mock := &mockAthenaClient{
				getQueryExecFn: func(ctx context.Context, params *athena.GetQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.GetQueryExecutionOutput, error) {
					if tc.execErr != nil {
						return nil, tc.execErr
					}
					idx := callCount
					if idx >= len(tc.execOutputs) {
						idx = len(tc.execOutputs) - 1
					}
					callCount++
					return tc.execOutputs[idx], nil
				},
			}

			// Use a context with a short timeout to avoid long sleeps in polling tests.
			// The sleep in waitForResults is 3 seconds, so we set timeout large enough
			// to allow a few iterations but small enough to keep tests reasonable.
			ctx := context.Background()
			err := waitForResults(ctx, mock, "test-query-id")

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error to contain %q, got %q", tc.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestHandleRequestWithClient(t *testing.T) {
	baseEvent := UpdateResourcesViewEvent{
		Catalog:  "test_catalog",
		Database: "test_db",
		Output:   "s3://test-bucket/output",
		View:     "aws_resources",
		Region:   "us-east-1",
	}

	tests := []struct {
		name              string
		event             UpdateResourcesViewEvent
		tableRows         [][]string // [table_name, has_region, has_tags, tags_data_type]
		startQueryErr     error
		getResultsErr     error
		startQueryCallIdx int // which call to StartQueryExecution should fail (0-indexed), -1 for none
		waitErr           bool
		wantErr           bool
		errContains       string
		checkSQL          func(t *testing.T, sql string)
	}{
		{
			name:  "happy path with region and varchar tags",
			event: baseEvent,
			tableRows: [][]string{
				{"aws_ec2_instances", "true", "true", "varchar"},
				{"aws_s3_buckets", "true", "true", "varchar"},
			},
			startQueryCallIdx: -1,
			wantErr:           false,
			checkSQL: func(t *testing.T, sql string) {
				if !strings.Contains(sql, "region as region") {
					t.Error("expected 'region as region' in SQL for table with region")
				}
				if !strings.Contains(sql, "tags as tags") {
					t.Error("expected 'tags as tags' in SQL for table with varchar tags")
				}
				if !strings.Contains(sql, "aws_ec2_instances") {
					t.Error("expected 'aws_ec2_instances' in SQL")
				}
				if !strings.Contains(sql, "aws_s3_buckets") {
					t.Error("expected 'aws_s3_buckets' in SQL")
				}
				if !strings.Contains(sql, "UNION ALL") {
					t.Error("expected 'UNION ALL' for multiple tables")
				}
			},
		},
		{
			name:  "tables missing region column",
			event: baseEvent,
			tableRows: [][]string{
				{"aws_global_resource", "false", "true", "varchar"},
			},
			startQueryCallIdx: -1,
			wantErr:           false,
			checkSQL: func(t *testing.T, sql string) {
				if !strings.Contains(sql, "'' as region") {
					t.Error("expected empty string region for table without region")
				}
			},
		},
		{
			name:  "tables with non-varchar tags",
			event: baseEvent,
			tableRows: [][]string{
				{"aws_ec2_instances", "true", "true", "map"},
			},
			startQueryCallIdx: -1,
			wantErr:           false,
			checkSQL: func(t *testing.T, sql string) {
				if !strings.Contains(sql, "'{}' as tags") {
					t.Error("expected '{}' as tags for non-varchar tags data type")
				}
			},
		},
		{
			name:  "tables without tags",
			event: baseEvent,
			tableRows: [][]string{
				{"aws_ec2_instances", "true", "false", ""},
			},
			startQueryCallIdx: -1,
			wantErr:           false,
			checkSQL: func(t *testing.T, sql string) {
				if !strings.Contains(sql, "'{}' as tags") {
					t.Error("expected '{}' as tags for table without tags")
				}
			},
		},
		{
			name:              "no tables found",
			event:             baseEvent,
			tableRows:         [][]string{},
			startQueryCallIdx: -1,
			wantErr:           true,
			errContains:       "no matching tables found",
		},
		{
			name:              "StartQueryExecution returns error on first call",
			event:             baseEvent,
			startQueryErr:     errors.New("start query error"),
			startQueryCallIdx: 0,
			wantErr:           true,
			errContains:       "start query error",
		},
		{
			name:          "GetQueryResults returns error",
			event:         baseEvent,
			getResultsErr: errors.New("get results error"),
			tableRows: [][]string{
				{"aws_ec2_instances", "true", "true", "varchar"},
			},
			startQueryCallIdx: -1,
			wantErr:           true,
			errContains:       "get results error",
		},
		{
			name: "with ExtraColumns set",
			event: UpdateResourcesViewEvent{
				Catalog:      "test_catalog",
				Database:     "test_db",
				Output:       "s3://test-bucket/output",
				View:         "aws_resources",
				Region:       "us-east-1",
				ExtraColumns: []string{"col1", "col2"},
			},
			tableRows: [][]string{
				{"aws_ec2_instances", "true", "true", "varchar"},
			},
			startQueryCallIdx: -1,
			wantErr:           false,
			checkSQL: func(t *testing.T, sql string) {
				if !strings.Contains(sql, "col1") {
					t.Error("expected 'col1' in SQL for extra columns")
				}
				if !strings.Contains(sql, "col2") {
					t.Error("expected 'col2' in SQL for extra columns")
				}
			},
		},
		{
			name:  "StartQueryExecution returns error on second call (view creation)",
			event: baseEvent,
			tableRows: [][]string{
				{"aws_ec2_instances", "true", "true", "varchar"},
			},
			startQueryErr:     errors.New("view creation error"),
			startQueryCallIdx: 1,
			wantErr:           true,
			errContains:       "view creation error",
		},
		{
			name:  "waitForResults returns error during view creation",
			event: baseEvent,
			tableRows: [][]string{
				{"aws_ec2_instances", "true", "true", "varchar"},
			},
			startQueryCallIdx: -1,
			waitErr:           true,
			wantErr:           true,
			errContains:       "query failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			startCallCount := 0
			var capturedViewSQL string
			getExecCallCount := 0

			mock := &mockAthenaClient{
				startQueryFn: func(ctx context.Context, params *athena.StartQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.StartQueryExecutionOutput, error) {
					currentCall := startCallCount
					startCallCount++
					if tc.startQueryErr != nil && currentCall == tc.startQueryCallIdx {
						return nil, tc.startQueryErr
					}
					// Capture the SQL from the second call (view creation)
					if currentCall == 1 {
						capturedViewSQL = *params.QueryString
					}
					return &athena.StartQueryExecutionOutput{
						QueryExecutionId: aws.String("test-query-id"),
					}, nil
				},
				getQueryExecFn: func(ctx context.Context, params *athena.GetQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.GetQueryExecutionOutput, error) {
					currentCall := getExecCallCount
					getExecCallCount++
					// For the waitErr case, fail on the second waitForResults call (view creation)
					if tc.waitErr && currentCall >= 1 {
						return &athena.GetQueryExecutionOutput{
							QueryExecution: &types.QueryExecution{
								Status: &types.QueryExecutionStatus{
									State: types.QueryExecutionStateFailed,
								},
							},
						}, nil
					}
					return succeededExecOutput(), nil
				},
				getResultsFn: func(ctx context.Context, params *athena.GetQueryResultsInput, optFns ...func(*athena.Options)) (*athena.GetQueryResultsOutput, error) {
					if tc.getResultsErr != nil {
						return nil, tc.getResultsErr
					}
					return buildQueryResultsOutput(tc.tableRows), nil
				},
			}

			ctx := context.Background()
			result, err := HandleRequestWithClient(ctx, mock, tc.event)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error to contain %q, got %q", tc.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result != "" {
					t.Fatalf("expected empty result, got %q", result)
				}
				if tc.checkSQL != nil {
					tc.checkSQL(t, capturedViewSQL)
				}
			}
		})
	}
}
