UPDATE addon_verifications SET transport_url = '';
DELETE FROM addon_verifications;

ALTER TABLE addon_verifications
    DROP COLUMN transport_url,
    ADD COLUMN transport_url_ciphertext bytea,
    ADD COLUMN transport_url_cipher_version smallint,
    ADD COLUMN transport_url_key_version integer,
    ADD CONSTRAINT addon_verifications_transport_url_encryption_check CHECK (
        (
            transport_url_ciphertext IS NULL
            AND transport_url_cipher_version IS NULL
            AND transport_url_key_version IS NULL
            AND (status = 'failed' OR consumed_at IS NOT NULL)
        )
        OR (
            status = 'passed'
            AND consumed_at IS NULL
            AND octet_length(transport_url_ciphertext) >= 29
            AND transport_url_cipher_version = 1
            AND transport_url_key_version > 0
        )
    );
