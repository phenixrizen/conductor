package dispatch

import "context"

// An attachment is a file of a ticket's, its content the file named by
// its hash beside the database, its row a member of the ticket's
// attachments set. Uploading, sweeping and serving the files come with
// their own change (docs/plans/dispatch.md, PR-7); a ticket's read
// carries its live attachments now.

// attachmentsOf reads the live attachments of the ticket with the id, in
// the order they were added.
func attachmentsOf(ctx context.Context, q queryer, ticketID string) ([]Attachment, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+attachmentColumns+" FROM attachments WHERE ticket_id = ? AND deleted_at = '' ORDER BY created_at, id", ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// attachmentColumns is an attachment's row as scanAttachment reads it.
const attachmentColumns = "id, ticket_id, name, size, sha256, content_type, created_at, created_by, deleted_at"

// scanAttachment reads an attachment's row.
func scanAttachment(row interface{ Scan(...any) error }) (Attachment, error) {
	var a Attachment
	var created, deleted string
	if err := row.Scan(&a.ID, &a.TicketID, &a.Name, &a.Size, &a.SHA256, &a.ContentType, &created, &a.CreatedBy, &deleted); err != nil {
		return Attachment{}, err
	}
	a.CreatedAt, a.DeletedAt = unstamp(created), unstamp(deleted)
	return a, nil
}
