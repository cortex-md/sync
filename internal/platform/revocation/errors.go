package revocation

import "errors"

var ErrRevoked = errors.New("access token revoked")
