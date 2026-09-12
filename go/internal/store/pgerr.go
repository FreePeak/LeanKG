package store

import "fmt"

func fmtPGNotReady(dsn string) error {
	return fmt.Errorf("store: postgres backend arrives with W4 (dsn %s accepted, implementation pending)", redactDSN(dsn))
}

// redactDSN strips credentials for error display.
func redactDSN(dsn string) string {
	for i := 0; i < len(dsn); i++ {
		if dsn[i] == '@' {
			schemeEnd := 0
			for j := 0; j < i; j++ {
				if dsn[j] == '/' {
					schemeEnd = j + 1
				}
			}
			return dsn[:schemeEnd] + "***@" + dsn[i+1:]
		}
	}
	return dsn
}
