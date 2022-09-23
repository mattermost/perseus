package perseus

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/jackc/pgproto3/v2"
	"github.com/jackc/pgtype"
)

func (s *Server) handleQueryMessage(ctx context.Context, schema string, c *Conn, msg *pgproto3.Query) error {
	log.Printf("received query: %q", msg.String)

	query := fmt.Sprintf("SET search_path='%s'; ", schema)
	query += msg.String
	// Execute query against database.
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return writeMessages(c,
			&pgproto3.ErrorResponse{Message: err.Error()},
			&pgproto3.ReadyForQuery{TxStatus: 'I'},
		)
	}
	defer rows.Close()

	// Encode column header.
	cols, err := rows.ColumnTypes()
	if err != nil {
		return fmt.Errorf("column types: %w", err)
	}
	buf := toRowDescription(cols).Encode(nil)

	// Iterate over each row and encode it to the wire protocol.
	for rows.Next() {
		row, err := scanRow(rows, cols)
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		buf = row.Encode(buf)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	// Mark command complete and ready for next query.
	buf = (&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")}).Encode(buf)
	buf = (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(buf)

	_, err = c.Write(buf)
	return err
}

func toRowDescription(cols []*sql.ColumnType) *pgproto3.RowDescription {
	var desc pgproto3.RowDescription
	for _, col := range cols {
		desc.Fields = append(desc.Fields, pgproto3.FieldDescription{
			Name:                 []byte(col.Name()),
			TableOID:             0,
			TableAttributeNumber: 0,
			DataTypeOID:          pgtype.TextOID,
			DataTypeSize:         -1,
			TypeModifier:         -1,
			Format:               0,
		})
	}
	return &desc
}

func scanRow(rows *sql.Rows, cols []*sql.ColumnType) (*pgproto3.DataRow, error) {
	refs := make([]any, len(cols))
	values := make([]any, len(cols))
	for i := range refs {
		refs[i] = &values[i]
	}

	// Scan from SQLite database.
	if err := rows.Scan(refs...); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	// Convert to TEXT values to return over Postgres wire protocol.
	row := pgproto3.DataRow{Values: make([][]byte, len(values))}
	for i := range values {
		row.Values[i] = []byte(fmt.Sprint(values[i]))
	}
	return &row, nil
}

func (s *Server) handleParseMessage(ctx context.Context, c *Conn, pmsg *pgproto3.Parse) error {
	return nil
	// // Rewrite system-information queries so they're tolerable by SQLite.
	// query := rewriteQuery(pmsg.Query)

	// if pmsg.Query != query {
	// 	log.Printf("query rewrite: %s", query)
	// }

	// // Prepare the query.
	// stmt, err := c.db.PrepareContext(ctx, query)
	// if err != nil {
	// 	return fmt.Errorf("prepare: %w", err)
	// }

	// var rows *sql.Rows
	// var cols []*sql.ColumnType
	// var binds []interface{}
	// exec := func() (err error) {
	// 	if rows != nil {
	// 		return nil
	// 	}
	// 	if rows, err = stmt.QueryContext(ctx, binds...); err != nil {
	// 		return fmt.Errorf("query: %w", err)
	// 	}
	// 	if cols, err = rows.ColumnTypes(); err != nil {
	// 		return fmt.Errorf("column types: %w", err)
	// 	}
	// 	return nil
	// }

	// // LOOP:
	// for {
	// 	msg, err := c.backend.Receive()
	// 	if err != nil {
	// 		return fmt.Errorf("receive message during parse: %w", err)
	// 	}

	// 	log.Printf("[recv(p)] %#v", msg)

	// 	switch msg := msg.(type) {
	// 	case *pgproto3.Bind:
	// 		binds = make([]interface{}, len(msg.Parameters))
	// 		for i := range msg.Parameters {
	// 			binds[i] = string(msg.Parameters[i])
	// 		}

	// 	case *pgproto3.Describe:
	// 		if err := exec(); err != nil {
	// 			return fmt.Errorf("exec: %w", err)
	// 		}
	// 		if _, err := c.Write(toRowDescription(cols).Encode(nil)); err != nil {
	// 			return err
	// 		}

	// 	case *pgproto3.Execute:
	// 		// TODO: Send pgproto3.ParseComplete?
	// 		if err := exec(); err != nil {
	// 			return fmt.Errorf("exec: %w", err)
	// 		}

	// 		var buf []byte
	// 		for rows.Next() {
	// 			row, err := scanRow(rows, cols)
	// 			if err != nil {
	// 				return fmt.Errorf("scan: %w", err)
	// 			}
	// 			buf = row.Encode(buf)
	// 		}
	// 		if err := rows.Err(); err != nil {
	// 			return fmt.Errorf("rows: %w", err)
	// 		}

	// 		// Mark command complete and ready for next query.
	// 		buf = (&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")}).Encode(buf)
	// 		buf = (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(buf)
	// 		_, err := c.Write(buf)
	// 		return err

	// 	default:
	// 		return fmt.Errorf("unexpected message type during parse: %#v", msg)
	// 	}
	// }
}
