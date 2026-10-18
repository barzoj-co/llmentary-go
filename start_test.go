// SPDX-License-Identifier: MIT

package llmtestlab

import (
	"context"
	"testing"
	"time"
)

func TestStartRequiresUpstream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Start(ctx); err == nil {
		t.Fatal("expected error when no upstream is configured")
	}
}
