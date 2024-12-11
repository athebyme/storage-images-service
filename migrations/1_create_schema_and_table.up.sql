CREATE SCHEMA IF NOT EXISTS storage;

CREATE TABLE IF NOT EXISTS storage.images (
    product_id int NOT NULL PRIMARY KEY,
    urls JSONB NOT NULL
);

CREATE TABLE storage.images_status (
   product_id int PRIMARY KEY,
   status VARCHAR(50) NOT NULL,
   timestamp TIMESTAMPTZ NOT NULL
);

-- Добавим индекс для ускорения запросов
CREATE INDEX idx_images_status_timestamp ON storage.images_status (timestamp);
CREATE INDEX idx_images_status_status ON storage.images_status (status);
