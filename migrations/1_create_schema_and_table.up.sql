CREATE SCHEMA IF NOT EXISTS storage;

CREATE TABLE IF NOT EXISTS storage.images (
    articular VARCHAR(255) NOT NULL PRIMARY KEY,
    urls JSONB NOT NULL
);
