package store

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// MySQL 候选集查询：参数为服务端时间与批量上限，返回按到期时间升序的订单主键。
func TestMySQLStore_PendingExpiredIDs(t *testing.T) {
	s, mk, _ := newMockStore(t)
	rows := sqlmock.NewRows([]string{"id"}).AddRow("NO-1").AddRow("NO-2")
	mk.ExpectQuery(regexp.QuoteMeta("SELECT id FROM orders")).
		WithArgs(mockNow, 500).
		WillReturnRows(rows)

	ids, err := s.PendingExpiredIDs(mockNow, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "NO-1" || ids[1] != "NO-2" {
		t.Fatalf("候选集不符：%v", ids)
	}
	if err := mk.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
