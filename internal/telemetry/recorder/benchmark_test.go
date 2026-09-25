package recorder

import (
	"testing"
	"time"
)

func BenchmarkNoopRepresentative(b *testing.B) {
	r := Noop{}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r.AddReceived(1, 64)
		r.SetDepth(3)
		r.Observe(OperationSend, time.Millisecond)
		r.RecordError(ErrorGeneric, 1)
	}
}
