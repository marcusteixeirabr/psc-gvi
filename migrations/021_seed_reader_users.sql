-- +goose Up
-- Adiciona dois usuários com papel reader.
-- Hashes gerados com bcrypt (custo 10) — nunca armazene senhas em texto puro.
INSERT INTO users (username, display_name, password_hash, role) VALUES
    ('trovao', 'Trovao',  '$2a$10$5kdOcHq2h7Cn4R7y603gQO5p9/cb0A9RWdEuqXZWGar19Icdq0vFO', 'reader'),
    ('vitor',  'Vitor',   '$2a$10$e3A6N7n08S.O2jVsxyEuHusG8ys542OLYihmc4HgjCEG1a/C/F.66',  'reader')
ON CONFLICT (username) DO NOTHING;

-- +goose Down
DELETE FROM users WHERE username IN ('trovao', 'vitor');
