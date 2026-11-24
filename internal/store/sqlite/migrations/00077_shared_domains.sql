-- +goose Up
-- Services of one project may share a domain, split by path (routes): core
-- checks who may use it, the index only speeds lookups.
DROP INDEX services_domain;
CREATE INDEX services_domain ON services (domain) WHERE domain <> '';

-- +goose Down
DROP INDEX services_domain;
CREATE UNIQUE INDEX services_domain ON services (domain) WHERE domain <> '';
