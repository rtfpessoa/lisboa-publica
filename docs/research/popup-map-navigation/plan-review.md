# Revisão independente do plano

2026-09-27. Checkpoint de planeamento, não de implementação. Revisor independente `/root/final_review`, instância de revisão já existente; não editou documentos, implementou, delegou ou executou testes. Recebeu plano, investigação, amostras, mapa e constraints/testes aceites da versão anterior. O agente principal criou e corrigiu todos os artefactos.

## Feedback e correções pelo principal

1. Calls com previsões precisava de expiração do resultado inteiro: agora fixa a menor ValidUntil, devolve410 entre páginas e tem teste de duas páginas/fallback planeado, sem filtering que altere membership.
2. Um vehicle_id não fixa o serviço clicado: agora vehicle_ref response-only liga revisão/asOf/observação e tuple exata, com validação e retry que não salta para nova viagem. A secção de referências à estação usa stop_id opcional efetivamente implementado no endpoint paginado de veículos, sem segundo endpoint novo.
3. Os horários retidos não têm todas as visitas nacionais: agora calls mostra regional_subset e nota de cobertura; fixture exterior→interior→exterior conserva extremos CP mas só visitas regionais clicáveis.
4. Cuidado de implementação incorporado: o helper projectVehicle/enrichVehicle existente não pode aplicar metadados de plano posterior à entidade congelada no novo handler. Não se abre um projeto separado de limpeza.

## Recomendação final do revisor

Aceitação do plano revisto, sem blockers restantes. Abrange investigação e planeamento apenas; validação humana da proposta de apresentação permanece pendente, e não implica aceitação de implementação/testes/deploy.

## Decisão do principal e verificações

O principal confirma que o plano cobre os casos pedidos, incorpora os blockers e define limites verificáveis. Aceita o plano como proposta pronta para implementação quando o utilizador a solicitar. O ticket HITL continua aberto, sem resposta humana inventada.

Verificados links locais, JSON das cinco amostras, dimensões limitadas de evidência e whitespace dos documentos. Apenas documentos/evidências novos; aplicação/configuração tracked inalteradas. Não há testes de aplicação novos nem mudanças de produção nesta etapa. Resultados de Go/race/browser/recursos anteriores são baseline, não prova destas futuras alterações.
