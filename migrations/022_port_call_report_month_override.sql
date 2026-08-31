-- +goose Up
-- Override manual de mês para escalas de fronteira (atracam no fim de um mês,
-- desatracam no início do mês seguinte). O relatório mensal e o KPI bucketizam
-- por actual_arrival; esse campo deixa o usuário decidir, caso a caso, que a
-- escala conte no mês da desatracação em vez disso — sem editar as datas reais
-- da escala. NULL = comportamento padrão (usa actual_arrival). Ver regras.md §13.
ALTER TABLE port_calls
    ADD COLUMN report_month_override DATE;

-- +goose Down
ALTER TABLE port_calls
    DROP COLUMN report_month_override;
