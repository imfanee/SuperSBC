package invoice

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPDF(t *testing.T) {
	s := &Service{Op: Operator{Name: "Acme Telecom Ltd", Address: "1 High Street|London|United Kingdom"}}
	start, end := Period(time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC))
	inv := &Invoice{
		ID: uuid.New(), Number: "INV-202608-000001", AccountID: uuid.New(), OwnerType: "customer", OwnerName: "acme",
		PeriodStart: start, PeriodEnd: end, Currency: "USD", Opening: "10.000000", Topups: "100.000000", Charges: "-42.123456",
		Adjustments: "0", Refunds: "0", Costs: "0", Closing: "67.876544", Calls: 1234, BilledSeconds: 86461, CreatedAt: end,
		Lines: []Line{{"UK Mobile", 1000, 80000, "-40.000000"}, {"USA", 234, 6461, "-2.123456"}},
	}
	inv.Amount, inv.Kind = "42.123456", "custom"
	out, err := s.PDF(inv, []Allocation{{Amount: "10.000000", ReceivedAt: end, Reference: "TRX-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-1.")) || len(out) < 1500 {
		t.Fatalf("not a pdf: %d bytes", len(out))
	}
	if start.Day() != 1 || end.Month() != time.September {
		t.Fatalf("period %s %s", start, end)
	}
	if hms(86461) != "24:01:01" || money("-42.123456", 2) != "-42.12" || negate("-1.5") != "1.5" {
		t.Fatal("formatting")
	}
}
