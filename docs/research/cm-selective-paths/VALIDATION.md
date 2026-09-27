# Percursos CM completos e seletivos — validação local

2026-09-27 · base 89fb91b9a6935efeb9e5555ae1d674e83775b215. Principal implementou, testou e corrigiu. Revisões independentes do plano e final por quasar-alpha/xhigh aceites; o principal confirma a aceitação local e o cumprimento do plano. Sem commit, push, deploy ou alterações de produção nesta etapa.

## Entrega

- O ID pattern publicado na posição CM é preservado, com namespace original de plano/agência. Não extraímos IDs da viagem nem inventamos data operacional.
- Cache estática partilha uma sequência verificada por pattern e referencia a geometria existente. Duas leituras streaming validam todas as viagens, independentemente da ordem; loops mantêm visitas distintas. Nenhum índice persistente por viagem ou geometria duplicada.
- Calls usam referência/revisão congelada e linha correspondente. Todas as paragens verificadas são `published_route`, `complete_published_route`, progresso desconhecido, sem horários/ETA. Páginas de 20; geometria opt-in só na página zero.
- Apenas o popup CM aberto pede estes dados. Destaque exato tem source/layer próprio no MapLibre, sem alterar GPS nem enquadrar o mapa em heartbeats. Fecho, estação, seleção, provider/tab ou overlay desligado limpam o destaque; requests usam AbortSignal.
- Controlo de percursos também dentro do popup permite alternar sem perder a página. Reativação numa página posterior sem geometria lê a página zero da revisão congelada.
- Sem associação segura, mantém-se a próxima paragem publicada, com aviso explícito; não se escolhe outra variante. Algumas paragens dos GTFS não existem no catálogo público atual; nesses casos a sequência não é anunciada como completa.
- Fonte única OpenAPI, Go oapi-codegen/v2 e TS oazapfts gerados. Não há upstream por clique, novo polling, APIs Mover, alteração de quotas, retenção, armazenamento ou snapshots com itinerários.

## Gates

| Check | Resultado | Evidência |
|---|---|---|
| Go race completo + Postgres local | PASS, 24,233 s | [log](postgres-race.txt) |
| Go race completo + Cockroach local | PASS, 270,834 s | [log](cockroach-race.txt) |
| Regressões finais CM/cache, race | PASS, 4,955 s, incluindo ingest original | [log](feature-regressions-race.txt) |
| Vet | PASS | [log](go-vet.txt) |
| Geração Go/TS sem drift | PASS | [log](generated-parity.txt) |
| TypeScript/build produção | PASS; aviso de chunks grandes já existente | [log](frontend-build.txt) |
| Browser estático combinado | 80/80 PASS, 2,9 min: mesmas 72 regressões + 8 CM | [log](browser-final.txt) |
| Envelope produção, todos os patterns e inventário original | PASS; 1031733248 B / 983,94 MiB RSS <1024 MiB | [log](resource-final.txt) |
| Maat normal | PASS, score87, regressões críticas[]; Go, TS pelo build/browser | [resultado](maat.json) |
| Diff whitespace | PASS | Principal executou git diff --check e git diff --cached --check |

A memória final usa o parser e cache de produção, 1621 patterns /57286 visitas, serialização/restore e refresh completo sobreposto. O inventário permanece1000 veículos por operador/tick, oito operadores,1hchurn,64revisões,20kbacklog; acrescenta-se pattern e linha a todos os1000 veículos CM, com strings próprias, mantendo os campos anteriores. Mantêm-se64snapshotsCP/110linhas,2decodes16776905bytes,duas leituras HTTP concorrentes paginadas, calls CM completos com geometria e páginas congeladas, e cancelamento de calls. GOMEMLIMIT768MiB/GOMAXPROCS2, container1280MiB. Compressão CM final≈2,59MB; admissão/cache e guard5GB permanecem ativos. Não se baixaram fixtures, caps ou retenção.

Parser/API cobrem ordem intercalada, loops, visitas duplicadas/em falta/extra, shape/direção diferente, stops/trips desconhecidos, CRC, ausência de pattern, namespace/plano/agência/linha incompatíveis, cache antiga sem campo, retenção atómica pattern/shape, geometry default false/só primeira página, bool inválido, referência adulterada/evictada, proveniência de stops e cancelamento. Checks de auth/scopes/admissão/quotas e cache JSON continuam na suite completa. Ingest CM mantém observação original e não cria dia/plano operacional.

Browser CM cobre desktop/mobile, variante exata diferente do overlay geral,20+5paragens, ausência de horas inventadas/placeholders, dados apenas para seleção aberta, paginação congelada, toggle, resposta de geometria atrasada após fecho, provider/tab, fallback de pattern sem associação e ausência de repost em heartbeats. MapLibre/Chromium são reais com respostas controladas. O atraso é libertado explicitamente, evitando depender de um timeout ou da cache anterior. As regressões originais incluem2261variantes/607609pontos e pixels/cliques DPR1/2.

## Correções da revisão final

A primeira revisão independente encontrou um bloqueio P2: TanStack não cancela uma query em curso quando muda apenas enabled para false. O principal acrescentou a visibilidade à identidade da query de geometria, consumindo AbortSignal; desligar desassocia o observer e aborta o pedido. A importação do resultado exige showPath ativo. Um novo browser check atrasa a resposta na página posterior, verifica requestfailed antes de a libertar, confirma que nada é retido e que reativar exige novo pedido da revisão correta.

O principal encontrou ainda um erro antigo dessa query que podia bloquear geometria válida ao voltar à primeira página. Erros são considerados apenas quando essa consulta está efetivamente necessária; há ação de recuperação no rodapé. Outro check verifica503 na geometria posterior e recuperação real na primeira página. [8/8 checks CM revistos](browser-review-regressions.txt) passaram; a suite combinada de 80 verificações também passou. Backend, spec, parser e gate RSS não mudaram nestas correções.

## Limites e tentativas corrigidas

A execução sem lista de ficheiros incluiu também quatro checks de dashboard que exigem backend/conta/dados preparados. O preview estático tinha proxy sem backend, produziu502 e essa execução foi interrompida; não é apresentada como aprovação. A suite final repete exatamente os seis ficheiros da aceitação anterior (72checks) e acrescenta cm-paths.spec.ts (8). Integração API/database usa handlers Go reais e bases locais separadamente; esta etapa não fez journey de conta autenticada nem verificações de produção.

O primeiro ensaio CM usava controlos exteriores, que fecham corretamente o popup. Foi acrescentado o controlo equivalente dentro dele. O primeiro ensaio de resposta atrasada reutilizava uma query fresca em cache; a regressão final abre uma página nova e bloqueia só a resposta geometry opt-in até o teste a libertar. Maat assinalou complexidade em parsing/construção/fixtures; principal separou validação de viagem, associação ao pattern e construção de linhas, sem registry/framework, supressões ou limiares alterados.

O índice completo anterior por trip atingia1065,52MiB e permanece apenas como experiência histórica. O novo índice por pattern publicado foi medido primeiro como protótipo972,83MiB e depois integrado; esses valores não substituem o gate final983,94MiB acima. Trata-se dos archives oficiais em cache e de uma amostra anterior389/389IDs encontrados, não de garantia de cobertura futura. Sem dados seguros, fallback explícito.

## Aceitação final

[Revisão independente final](final-review.md), executada com quasar-alpha / xhigh / read-only, recomenda aceitação local sem bloqueios nem observações adicionais. O principal confirma os requisitos do plano: sequência publicada verificada, variante seletiva, paginação e navegação congeladas, cancelamento e fallback explícito, sem upstream adicional nem aumento dos limites. Os 80 checks de browser, race nas duas bases, contratos gerados, build/vet, Maat e envelope de memória passaram. Aceitação local; sem commit, push ou deploy nesta etapa.
