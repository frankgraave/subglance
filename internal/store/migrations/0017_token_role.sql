-- 0017_token_role.sql — a token may carry less authority than its owner.
--
-- A token used to act with its owner's full role, so an administrator who
-- wanted a read-only key for a dashboard had to hand out an administrator key.
-- The role here is a ceiling, not a grant: a token acts with the lower of this
-- and its owner's current role, so demoting a user demotes their tokens too.
--
-- NULL means "the owner's role", which is what every token created before this
-- column existed did, so no existing token changes what it may do.
ALTER TABLE api_tokens
    ADD COLUMN role TEXT CHECK (role IN ('admin', 'editor', 'viewer'));
