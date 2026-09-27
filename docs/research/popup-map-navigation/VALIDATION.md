# Aceitação local — popups, nomes e navegação no mapa

2026-09-27 · base `89fb91b9a6935efeb9e5555ae1d674e83775b215` · agente principal responsável por implementação, testes e correções.

Estado: **aceite localmente pelo agente principal**, após recomendação independente incondicional de `quasar-alpha/xhigh`, sem bloqueios. Não houve commit, push, deploy ou alteração da configuração de produção nesta etapa.

## Resultado e correspondência ao pedido

| Pedido | Entrega e limites |
|---|---|
| Estação e veículo sobrepostos | Separação apenas no ecrã, conector para a posição original, alvo de 44 px e seletor para grupos densos. As coordenadas GPS, relógios e métricas não mudam. |
| Ícones por transporte | Glifos locais de autocarro, barco, Metro e comboio, sem serviço de imagens externo. Operador e estado mantêm as suas distinções. |
| Avisos repetidos | Estado factual compacto; avisos únicos no rodapé; botão acessível no cabeçalho faz scroll e foca o rodapé. Estimativa Metro continua identificada no topo. |
| Campos sem dados | Campos opcionais ausentes/ vazios são omitidos. Zero é preservado; booleano falso não é promovido a garantia de ausência. Capacidades são publicadas, não lugares livres. |
| Enriquecimento verificável | Número comercial GTFS chega às entidades, viagens e chegadas. Lugares legados Mobi apenas para agência 21 e unidade exata, respeitando precedência. Identidades físicas ambíguas CP/Metro/Fertagus não ganham modelos ou capacidades inventados. |
| Números e nomes CP | Número comercial quando publicado; AP, IC, R e IR apresentam Alfa Pendular, Intercidades, Regional e InterRegional. Sem número, conserva-se identificação legível sem extrair um número do ID interno. |
| Linhas Metro | Nomes completos e cores; abreviaturas distintas Az/Am/Vd/Vm em círculos, evitando duas letras A/V indistinguíveis sem cor. |
| Idade do registo | Hora Lisboa e idade relativa calculada sobre a observação original, incluindo validação de horas inválidas/futuras. Refetch não renova a observação. |
| Estação → veículo → paragem | Cliente gerado, referências congeladas e joins únicos por operador/plano/viagem/dia. Catálogo e operador da paragem são verificados na mesma revisão e com timestamp estático. Origem é preservada em erro/expiração/mismatch. |
| Percurso CM | Próxima paragem explicitamente reportada, clicável e rotulada. O índice completo foi medido e rejeitado por exceder o gate RSS; fallback já previsto no plano. |

## Verificações e evidências

Todas as execuções foram realizadas pelo agente principal. Browser usa Chromium/MapLibre reais com respostas API controladas e basemap vazio para não depender de rede externa; provas do contrato/backend usam handlers Go reais e bases locais.

| Verificação | Resultado | Evidência |
|---|---|---|
| `go test ./...` | Passou; os testes com DB são executados separadamente abaixo | Execução local, seguida das suites race com DB |
| `go test -race ./...`, Postgres local | Passou, 21,832 s | [log](validation/postgres-full-race.txt) |
| `go test -race ./...`, Cockroach local | Passou, 277,545 s | [log](validation/cockroach-full-race.txt) |
| Regressões finais Postgres / Cockroach | Passaram após últimas correções de relógio, fallback e cancelamento | [Postgres](validation/postgres-final-regressions.txt), [Cockroach](validation/cockroach-final-regressions.txt) |
| `go vet ./...` | Passou, sem output | [log](validation/go-vet.txt) |
| `make check-generated` | Passou; Go e TS correspondem à mesma OpenAPI | [log](validation/generated-parity.txt) |
| `npm run build` | TypeScript e build de produção passaram | [log](validation/frontend-build.txt) |
| Browser combinado, build de produção estático | **72/72 passaram**, 2,5 min, após correção de renderização; sem hot reload nem runners sobrepostos | [log final](validation/browser-static-final.txt) |
| Ícones realmente desenhados, DPR1/2 | Passou, 2/2, pixels no ponto deslocado, cliques e ausência de avisos do layer | [log](validation/rendered-targets.txt) |
| Geometria oficial completa desktop/mobile | Passou; 2261 variantes / 607609 pontos, heap abaixo do gate 512 MiB e dados inalterados não reenviados ao worker a cada heartbeat | [log](validation/geometry-browser.txt); repetida na suite combinada |
| Envelope de recursos com leituras concorrentes | Passou; RSS 996769792 B / 950,59 MiB, abaixo de 1024 MiB | [log final](validation/resource-final.txt) |
| Maat normal, sem bypass | Passou, score 87, zero regressões críticas; cobre Go, TypeScript verificado por build/browser | [resultado](validation/maat.json) |
| `git diff --check` | Passou | Execução local |

A suite backend inclui os oito operadores, planos e dias diferentes, referências alteradas/expiradas, ambiguidade de entidades e plataformas, parent stops, loops sem progresso, percurso regional CP com extremos nacionais separados, paginação e menor expiração da coleção CP, rejeição de chave sem scope `read:transit`, admissão de leituras caras, cancelamento, relógio após publicação e validação final depois de trabalho de fallback. DTOs de navegação não são persistidos em posições/snapshots. Cache GTFS JSON legado e sequências grandes permanecem compatíveis; overflow dos relógios é rejeitado.

Browser cobre ida/volta nos quatro modos, close/outside/Escape, avisos/foco, campos zero/falso, nomes, idade, ligação 410 sem salto ao ID mais recente, cancelamento ao fechar, plano estático substituído, ausência normal de plano CM, novo registo do mesmo serviço, referências antigas/erro na estação e foco no seletor denso durante heartbeat. Os dois DPR verificam os cliques independentes estação/veículo, conector, coordenadas originais e remoção de offsets com a camada de paragens desligada.

## Medição de memória e decisão CM

Configuração invariável: `GOMEMLIMIT=768MiB`, `GOMAXPROCS=2`, RSS máximo local 1024 MiB, container de produção 1280 MiB. Workload completo de oito operadores: 64 revisões retidas, backlog 20000, 1000 veículos por operador/tick, churn durante 1 h, refresh estático completo sobreposto de 11 archives, 64 snapshots CP distintos de 110 linhas e dois decodes próximos de 16 MiB. A versão final acrescenta duas leituras HTTP concorrentes paginadas (veículos e calls) e prova de cancelamento.

| Variante | Pico RSS | Decisão |
|---|---:|---|
| HEAD exato, com o mesmo pior caso CP completo | 1169866752 B / 1115,67 MiB | Acima do gate; a medição antiga sem todo este workload não serve de controlo |
| Final, leitura concorrente anterior | 999440384 B / 953,14 MiB | Passou |
| Final, binário exato da aceitação | 996769792 B / 950,59 MiB | Passou; gate externo ≤1024 MiB |
| Candidato opt-in com índice exato CM e refresh sobreposto | 1117274112 B / 1065,52 MiB | **Rejeitado pelo RSS**, apesar de o teste lógico Go passar |

[Controlo HEAD](validation/resource-baseline.txt) · [candidato CM](validation/resource-cm-candidate.txt) · [leituras concorrentes](validation/resource-with-concurrent-reads.txt) · [dimensão exata CM](validation/cm-exact-path-sizing.json).

O candidato CM contém 266184 viagens, 1621 variantes exatas e 57286 visitas ordenadas; preserva trip/plano/agência e deduplica apenas sequências equivalentes. Não é publicado no runtime. A correção de memória aplicada é limitada: slices de visitas com capacidade exata e relógios GTFS até 72 h em `int32`, mantendo `stop_sequence` em `int`. Não se diminuiu fixture, retenção, cap, validação de archives, frequência ou cobertura para passar.

## Revisões independentes e correções

O plano foi revisto por `quasar-alpha` com `xhigh` antes do “go”: [revisão](plan-review.md). O mesmo modelo/effort foi usado no checkpoint final, exclusivamente como revisor read-only. Nenhum subagente implementou ou testou o seu próprio trabalho.

| Bloqueio identificado | Correção do principal |
|---|---|
| Expiração e cancelamento durante processamento | Relógio fresco na seleção e ao devolver; coleção CP expira globalmente; validação adicional após construir fallback; regressão cancela nesse ponto tardio. |
| Observação nova junto de percurso antigo | Primeira página volta a associar ao novo registo do mesmo serviço; páginas congeladas identificam hora original e oferecem atualização explícita. |
| Referências da estação antigas ou fonte em erro | Qualificação por idade/erro, último registo e expiração 1 h; estado desconhecido não é descrito como atual/parado. |
| Paragem antiga aberta sobre rede nova | `stop_plan_id` / `stop_static_updated_at` no contrato; catálogo e operador lidos na mesma revisão; verifica nome/ID/posição/proveniência antes de saltar. |
| Ausência de plano CM `undefined`/`null` | Normalização simétrica, sem dispensar identidade/timestamp; regressão browser da próxima paragem CM. |
| Filtro incompatível | Limpa apenas filtro de linha que exclui a paragem; preserva filtros compatíveis. |
| Foco do seletor durante heartbeat | Membership estável; foco apenas na abertura, sem reset durante atualização. |
| Lugares Mobi sem agência verificável | Exige `agency_id=21`, unidade exata, inteiro válido e precedência de metadata; valores de outra agência/ausentes rejeitados. |

A primeira suite estática terminou com 71/72: o teste DPR1 usava `mouse.click` por coordenadas enquanto a sidebar ainda animava o fecho de 200 ms, atingindo o botão CP. O trace mostrou a seleção ficar vazia. A correção é apenas no teste: esperar geometricamente pelo lado direito da sidebar ≤0 antes do clique; mantém todos os asserts e não usa delay arbitrário. O revisor confirmou esta correção apropriada. A nova execução completa é a evidência final; não se apresenta a tentativa falhada como aprovação.

Todos os gates passaram, incluindo a última suite completa com a verificação dos pixels renderizados. O revisor recomenda aceitação local incondicional e o principal confirma o cumprimento do plano/pedidos e das limitações aprovadas: [recomendação e decisão final](validation/final-review.md).

A inspeção dos logs do servidor local revelou ainda um bloqueio de renderização: propriedades GeoJSON array eram serializadas como string, pelo que a área clicável deslocava mas o sprite podia ficar no ponto original. O principal substituiu o array por duas propriedades escalares e uma expressão `semiliteral` com assertions numéricas, suportada na versão instalada; `stale` é agora estritamente booleano. O primeiro ensaio sem assertion numérica foi rejeitado pela própria validação de estilo (`array<value>` vs `array<number>`), e foi corrigido antes da aceitação. Não se alteraram dependências. A fixture DPR passa a verificar pixels verdes do glyph no ponto deslocado, além dos cliques e ausência de warning de `vehicle-points`/aviso de erro do mapa. Esta verificação expôs a falha que o teste apenas de hit targets não detetava. [Especificação oficial da expressão](https://maplibre.org/maplibre-style-spec/expressions/#semiliteral).

## Inspeção visual e limites

[Desktop](validation/popup-desktop.png) · [Mobile DPR2](validation/popup-mobile.png) · [Sobreposição DPR1](validation/overlap-dpr1.png) · [Sobreposição DPR2](validation/overlap-dpr2.png). Capturas de fixtures, não de cobertura real dos fornecedores; o fundo vazio é intencional. O principal inspecionou as capturas: título/idade/estado legíveis, aviso acessível, próximo destino clicável, fecho visível e painel scrollável. O build mantém o aviso existente de chunk MapLibre >1000 kB; sem introduzir dependências, alterar limiar ou abrir esforço de bundling.

Esta aceitação é local. Não valida novas chamadas autenticadas nem garante que o fornecedor tenha posições ou specs para cada serviço. Silêncio/proximidade não provam paragem; esperas diretas Metro sem identidade cruzada continuam sem ligação inventada; CP conserva só visitas regionais disponíveis; loops sem progresso publicado mostram percurso com qualificação. Não foram usadas APIs Mover nem aumentados polling, quotas, armazenamento ou snapshots. Deploy e verificação de produção não foram executados nesta etapa.
