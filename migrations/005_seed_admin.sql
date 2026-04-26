-- +goose Up
-- -----------------------------------------------------------------------------
-- Seed: usuário admin inicial
--
-- Cria o único usuário admin do sistema para que seja possível fazer o
-- primeiro login e cadastrar os demais usuários pela interface.
--
-- SENHA: a senha abaixo é o hash bcrypt de "admin123".
-- IMPORTANTE: troque a senha no primeiro login!
--
-- Como gerar um novo hash bcrypt se precisar resetar:
--   No Go:  bcrypt.GenerateFromPassword([]byte("nova_senha"), bcrypt.DefaultCost)
--   Online: https://bcrypt-generator.com (use cost=10)
--
-- Por que não usamos a senha em texto puro aqui?
--   Este arquivo fica no repositório Git (público).
--   Nunca armazene senhas em texto puro — nem em seeds.
-- -----------------------------------------------------------------------------
INSERT INTO users (username, display_name, password_hash, role, active)
VALUES (
    'admin',
    'Administrador',
    -- Hash bcrypt da senha "admin123" com custo 10.
    -- Gerado com: bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
    -- O bcrypt inclui o salt dentro do próprio hash, portanto é seguro
    -- armazenar diretamente no banco.
    '$2a$10$Rdw7FdqlZ3hlqwFsTkyde.c8AnrrWC633oUWcVbGPPHXZzo.3yiFq',
    'admin',
    true
);

-- +goose Down
-- Remove apenas o admin seed — não afeta usuários criados posteriormente.
DELETE FROM users WHERE username = 'admin';
