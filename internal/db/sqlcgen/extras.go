package sqlcgen

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// DateToTimePtr returns nil when the date is NULL, else a copy of the date as *time.Time.
func DateToTimePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

// TimeToDate converts time.Time to pgtype.Date (always Valid).
func TimeToDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

// TimePtrToDate returns an invalid (NULL) pgtype.Date when t is nil.
func TimePtrToDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{Valid: false}
	}
	return pgtype.Date{Time: *t, Valid: true}
}
