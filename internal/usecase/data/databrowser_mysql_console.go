package data

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// mysqlConsole runs sql with the mysql client, as the read-only user unless
// write is set; only the last result set shows. Output is read as XML, where
// NULL and text are told apart.
func (t dataTarget) mysqlConsole(ctx context.Context, sql string, write bool) (ConsoleResult, error) {
	var res ConsoleResult
	var sets int
	var set *ConsoleResult
	err := t.execStream(ctx, t.mysql(sql, write, dataConsoleTimeout, nil, "--xml", "--binary-as-hex"), func(r io.Reader) error {
		lr := &io.LimitedReader{R: r, N: dataOutputMax}
		dec := xml.NewDecoder(&xmlSafeReader{r: lr})
		var field *string
		var inField bool
		for {
			tok, err := dec.Token()
			switch {
			case lr.N <= 0:
				res.More = true
				return dropRest(r, write)
			case errors.Is(err, io.EOF):
				return nil
			case err != nil:
				return fmt.Errorf("mysql output: %w", err)
			}
			switch el := tok.(type) {
			case xml.StartElement:
				switch el.Name.Local {
				case "resultset":
					sets++
					set = &ConsoleResult{Rows: [][]*string{}, Truncated: [][2]int{}}
				case "row":
					if set == nil {
						continue
					}
					if len(set.Rows) == DataPageMax {
						set.More = true
						continue
					}
					set.Rows = append(set.Rows, []*string{})
				case "field":
					if set == nil || set.More || len(set.Rows) == 0 {
						continue
					}
					name, null := "", false
					for _, a := range el.Attr {
						switch a.Name.Local {
						case "name":
							name = a.Value
						case "nil":
							null = a.Value == "true"
						}
					}
					row := &set.Rows[len(set.Rows)-1]
					if len(set.Rows) == 1 {
						set.Columns = append(set.Columns, name)
					}
					inField, field = !null, nil
					if !null {
						field = new(string)
					}
					*row = append(*row, field)
				}
			case xml.CharData:
				if inField && field != nil {
					*field += string(el)
				}
			case xml.EndElement:
				if el.Name.Local == "field" {
					if inField && field != nil {
						s, cut := truncate(*field)
						*field = s
						if cut {
							set.Truncated = append(set.Truncated, [2]int{len(set.Rows) - 1, len(set.Rows[len(set.Rows)-1]) - 1})
						}
					}
					inField = false
				}
				if el.Name.Local == "resultset" && set != nil {
					res.Columns, res.Rows, res.Truncated = set.Columns, set.Rows, set.Truncated
					res.More = res.More || set.More
				}
			}
		}
	})
	if err != nil {
		return ConsoleResult{}, t.mysqlWriteHint(err, write)
	}
	switch {
	case sets == 0:
		res.Output = "OK"
	case len(res.Rows) == 0:
		res.Columns, res.Rows, res.Output = nil, nil, "0 rows"
	}
	return res, nil
}

// mysqlWriteHint says why the read-only user was refused.
func (t dataTarget) mysqlWriteHint(err error, write bool) error {
	err = clientError(err)
	if !write && err != nil && (strings.Contains(err.Error(), "command denied to user") || strings.Contains(err.Error(), "Access denied for user")) {
		return fmt.Errorf("%w (read only: allow writes to change data)", err)
	}
	return err
}
