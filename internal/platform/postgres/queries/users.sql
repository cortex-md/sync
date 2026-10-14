-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, display_name, public_key, email_verified_at, avatar_key, locale, is_instance_admin,
    kdf_version, kdf_salt, encrypted_private_key, recovery_wrapped_private_key, created_at, updated_at)
VALUES (@id, @email, @password_hash, @display_name, @public_key, @email_verified_at, @avatar_key, @locale,
        NOT EXISTS (SELECT 1 FROM users), @kdf_version, @kdf_salt, @encrypted_private_key, @recovery_wrapped_private_key,
        @created_at, @updated_at)
RETURNING is_instance_admin;

-- name: LockRegistrations :exec
SELECT pg_advisory_xact_lock(hashtext('cortex.registrations'));

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(@email::text);

-- name: ListUsersByIDs :many
SELECT * FROM users WHERE id = ANY(@ids::uuid[]);

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UpdateUserPublicKey :execrows
UPDATE users SET public_key = @public_key, updated_at = now() WHERE id = @id;

-- name: UpdateUser :execrows
UPDATE users SET email = @email, password_hash = @password_hash, display_name = @display_name,
    public_key = @public_key, email_verified_at = @email_verified_at, avatar_key = @avatar_key,
    locale = @locale, kdf_version = @kdf_version, kdf_salt = @kdf_salt,
    encrypted_private_key = @encrypted_private_key, recovery_wrapped_private_key = @recovery_wrapped_private_key,
    updated_at = now()
WHERE id = @id;

-- name: CreateUserToken :exec
INSERT INTO user_tokens (id, user_id, kind, token_hash, payload, expires_at, created_at)
VALUES (@id, @user_id, @kind, @token_hash, @payload, @expires_at, @created_at);

-- name: ConsumeUserToken :one
UPDATE user_tokens SET used_at = now()
WHERE token_hash = @token_hash AND kind = @kind AND used_at IS NULL AND expires_at > now()
RETURNING *;

-- name: GetActiveUserToken :one
SELECT * FROM user_tokens
WHERE token_hash = @token_hash AND kind = @kind AND used_at IS NULL AND expires_at > now();

-- name: DeleteUserTokensByKind :exec
DELETE FROM user_tokens WHERE user_id = @user_id AND kind = @kind;

-- name: DeleteExpiredUserTokens :execrows
DELETE FROM user_tokens WHERE expires_at < now() OR used_at IS NOT NULL;
