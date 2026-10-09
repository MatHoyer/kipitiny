-- +goose Up
-- Where nodes of the map canvas were dragged to, shared by every user.
-- node is "<kind>:<id>" (e.g. svc:<service id>, project:<project id>);
-- x and y are relative to the node's parent frame, if any.
CREATE TABLE canvas_positions (
    node       TEXT PRIMARY KEY,
    x          REAL NOT NULL,
    y          REAL NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE canvas_positions;
