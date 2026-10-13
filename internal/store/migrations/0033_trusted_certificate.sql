-- 0033_trusted_certificate.sql — let a monitor trust one certificate.
--
-- An appliance with a self-signed certificate, or a service behind a private
-- CA, fails every https and ssl check: nothing in the trust store vouches for
-- it. A pin says which certificate to trust instead, on one monitor:
--
--   trusted_cert_sha256  the SHA-256 fingerprint of the certificate's DER
--                        encoding, as upper-case hex pairs joined by colons
--                        (95 characters). NULL trusts the trust store alone,
--                        which is every existing row.
--   trusted_cert_at      when it was pinned, in Unix seconds, so a check that
--                        is presented another certificate can say since when
--                        the old one was trusted.
--
-- The CHECKs keep a pin on http and ssl monitors only, keep its spelling to
-- the one the checker compares, and make the two columns one fact. Whether an
-- http monitor's target is https is checked in Go, where the refusal can name
-- the field.
ALTER TABLE monitors ADD COLUMN trusted_cert_sha256 TEXT
    CHECK (trusted_cert_sha256 IS NULL OR
           (type IN ('http', 'ssl') AND length(trusted_cert_sha256) = 95));
ALTER TABLE monitors ADD COLUMN trusted_cert_at INTEGER
    CHECK ((trusted_cert_at IS NULL) = (trusted_cert_sha256 IS NULL));
