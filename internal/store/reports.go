package store

import "database/sql"

// ReportByName returns the winners of one prefix ordered by last name, first
// name and phone number. Baskets without a drawn winner sort first.
func (s *Store) ReportByName(prefix string) ([]ReportByNameLine, error) {
	rows, err := s.db.Query(`SELECT last_name, first_name, phone_number, pref, prefix, b_id, description, donors, winning_ticket
		FROM report_by_name WHERE prefix = ? ORDER BY last_name, first_name, phone_number, prefix, b_id`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportByNameLine{}
	for rows.Next() {
		var l ReportByNameLine
		var last, first, phone, pref, desc, donors sql.NullString
		var id, winning sql.NullInt64
		if err := rows.Scan(&last, &first, &phone, &pref, &l.Prefix, &id, &desc, &donors, &winning); err != nil {
			return nil, err
		}
		l.LastName, l.FirstName, l.PhoneNumber, l.Pref = nstr(last), nstr(first), nstr(phone), nstr(pref)
		l.Description, l.Donors = nstr(desc), nstr(donors)
		l.BID, l.WinningTicket = nint(id), nint(winning)
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReportByBasket returns the winners of one prefix ordered by basket id.
func (s *Store) ReportByBasket(prefix string) ([]ReportByBasketLine, error) {
	rows, err := s.db.Query(`SELECT prefix, b_id, description, donors, winning_ticket, last_name, first_name, phone_number, pref
		FROM report_by_basket WHERE prefix = ? ORDER BY prefix, b_id`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportByBasketLine{}
	for rows.Next() {
		var l ReportByBasketLine
		var desc, donors, last, first, phone, pref sql.NullString
		var id, winning sql.NullInt64
		if err := rows.Scan(&l.Prefix, &id, &desc, &donors, &winning, &last, &first, &phone, &pref); err != nil {
			return nil, err
		}
		l.Description, l.Donors = nstr(desc), nstr(donors)
		l.LastName, l.FirstName, l.PhoneNumber, l.Pref = nstr(last), nstr(first), nstr(phone), nstr(pref)
		l.BID, l.WinningTicket = nint(id), nint(winning)
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReportCounts returns unique buyers and total buys per prefix, followed by
// the total row (IsTotal).
func (s *Store) ReportCounts() ([]ReportCountLine, error) {
	rows, err := s.db.Query(`SELECT prefix, is_total, unique_buyers, total_buys FROM report_counts ORDER BY is_total, prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportCountLine{}
	for rows.Next() {
		var l ReportCountLine
		var prefix sql.NullString
		var unique, total sql.NullInt64
		if err := rows.Scan(&prefix, &l.IsTotal, &unique, &total); err != nil {
			return nil, err
		}
		l.Prefix, l.UniqueBuyers, l.TotalBuys = nstr(prefix), nint(unique), nint(total)
		out = append(out, l)
	}
	return out, rows.Err()
}
