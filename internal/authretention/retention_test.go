package authretention

import (
	"database/sql"
	"testing"
)

func TestNormalizeOptions(t *testing.T) {
	tests := []struct {
		name    string
		input   Options
		want    Options
		wantErr bool
	}{
		{
			name:  "zero values use defaults",
			input: Options{},
			want: Options{
				RetentionDays: DefaultRetentionDays,
				BatchSize:     DefaultBatchSize,
				MaxBatches:    DefaultMaxBatches,
			},
		},
		{
			name:  "explicit bounds are preserved",
			input: Options{RetentionDays: 30, BatchSize: 10, MaxBatches: 20},
			want:  Options{RetentionDays: 30, BatchSize: 10, MaxBatches: 20},
		},
		{
			name:  "minimum values are accepted",
			input: Options{RetentionDays: MinRetentionDays, BatchSize: 1, MaxBatches: 1},
			want:  Options{RetentionDays: MinRetentionDays, BatchSize: 1, MaxBatches: 1},
		},
		{
			name:  "maximum values are accepted",
			input: Options{RetentionDays: MaxRetentionDays, BatchSize: MaxBatchSize, MaxBatches: MaxMaxBatches},
			want:  Options{RetentionDays: MaxRetentionDays, BatchSize: MaxBatchSize, MaxBatches: MaxMaxBatches},
		},
		{
			name:    "negative retention days rejected",
			input:   Options{RetentionDays: -1},
			wantErr: true,
		},
		{
			name:    "retention days above maximum rejected",
			input:   Options{RetentionDays: MaxRetentionDays + 1},
			wantErr: true,
		},
		{
			name:    "negative batch size rejected",
			input:   Options{BatchSize: -1},
			wantErr: true,
		},
		{
			name:    "batch size above maximum rejected",
			input:   Options{BatchSize: MaxBatchSize + 1},
			wantErr: true,
		},
		{
			name:    "negative max batches rejected",
			input:   Options{MaxBatches: -1},
			wantErr: true,
		},
		{
			name:    "max batches above maximum rejected",
			input:   Options{MaxBatches: MaxMaxBatches + 1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeOptions(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeOptions(%+v) returned nil error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeOptions(%+v): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeOptions(%+v) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveRetentionDays(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "unset uses default", raw: "", want: DefaultRetentionDays},
		{name: "whitespace uses default", raw: "  \t ", want: DefaultRetentionDays},
		{name: "minimum", raw: "1", want: 1},
		{name: "maximum", raw: "365", want: 365},
		{name: "trimmed value", raw: " 30 ", want: 30},
		{name: "zero rejected", raw: "0", wantErr: true},
		{name: "above maximum rejected", raw: "366", wantErr: true},
		{name: "negative rejected", raw: "-1", wantErr: true},
		{name: "non integer rejected", raw: "abc", wantErr: true},
		{name: "decimal rejected", raw: "7.5", wantErr: true},
		{name: "exponent rejected", raw: "1e2", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveRetentionDays(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveRetentionDays(%q) returned nil error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRetentionDays(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ResolveRetentionDays(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestRunRejectsNilDatabase(t *testing.T) {
	_, err := Run(t.Context(), nil, Options{})
	if err == nil {
		t.Fatal("Run(nil database) returned nil error")
	}
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	_, err := Run(t.Context(), new(sql.DB), Options{BatchSize: MaxBatchSize + 1})
	if err == nil {
		t.Fatal("Run returned nil error for oversized batch")
	}
}
