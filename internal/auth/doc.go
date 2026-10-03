// Package auth is the API's authentication (plan §2.16): one admin account whose password is
// stored as an argon2id hash, API tokens stored only as hashes (the value is shown once at
// creation), browser sessions with a CSRF token, the one-time setup token of the first start and
// the rate limit of the login. The state is a file with mode 0600 in a directory with mode 0700,
// written atomically; `chaosgw admin reset-password` changes it from outside the API process, and
// the running API notices (sessions of an older password epoch end).
package auth
