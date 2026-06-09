-- check_mutes: operator-set suppression of a (server_id, check_id[, object]).
-- Co-located in the stats DB so the ingestion scheduler needs only its
-- stats pool. Small operational table — not partitioned. A mute with
-- object='' applies to every object of that check on that server.
CREATE TABLE check_mutes (
    server_id   TEXT        NOT NULL,
    check_id    TEXT        NOT NULL,
    object      TEXT        NOT NULL DEFAULT '',
    muted_until TIMESTAMPTZ NOT NULL,
    reason      TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (server_id, check_id, object)
);
