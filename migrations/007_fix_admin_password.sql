-- +goose Up
-- Corrige o hash bcrypt do usuário admin.
-- O hash anterior era de uma senha diferente (erro no seed inicial).
-- Este hash corresponde à senha "admin123".
-- Troque a senha no primeiro login via interface.
UPDATE users
SET password_hash = '$2a$10$Rdw7FdqlZ3hlqwFsTkyde.c8AnrrWC633oUWcVbGPPHXZzo.3yiFq'
WHERE username = 'admin';

-- +goose Down
-- Não há rollback significativo aqui — apenas registramos a intenção.
-- Se precisar resetar, gere um novo hash com bcrypt.GenerateFromPassword.
