-- A rule can require that all CI checks of the head commit pass before merging.
ALTER TABLE branch_protections ADD COLUMN require_checks INTEGER NOT NULL DEFAULT 0 CHECK (require_checks IN (0, 1));
