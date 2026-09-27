# Plano proposto: popups claros e navegação entre veículos e paragens

2026-09-27 · base `89fb91b` · **plano aprovado (“go”); implementação concluída e aceite localmente após revisão independente**.

[Revisão independente: recomenda aceitação do plano](research/popup-map-navigation/plan-review.md) · [Mapa Wayfinder](/Users/rodrigo.fernandes/docs/wayfinder/popup-map-navigation/MAP.md) · [investigação e fontes](research/POPUP-MAP-NAVIGATION.md).

## Resultado pretendido

Um veículo e uma estação sobrepostos continuam ambos acessíveis. Os ícones distinguem autocarro, barco, Metro e comboio. Os popups privilegiam serviço, estado, idade e informação disponível; avisos ficam num único bloco inferior acessível a partir do cabeçalho. Estações ligam a veículos identificados; veículos ligam às suas paragens publicadas. Os títulos usam nomes comerciais legíveis, cores Metro e o número CP quando publicado.

Este documento cobre todos os casos pedidos. O utilizador autorizou a execução do plano com “go”. A implementação e validação pertencem ao agente principal; a revisão independente permanece obrigatória antes da aceitação.

## Restrições que permanecem

- UI pública, Metro único operador inicial; não mudar seleção inicial, tabs, auth, overlay defaults nem limites de API. Não usar APIs Mover.
- Fonte original e relógio original preservados: fonte fresca até 90 s; posição corrente até 180 s; inativo após 5 min; última posição retida até 1 h. Posição retida não entra em métricas correntes, velocidade ou contagem atual. Silêncio e proximidade não provam paragem.
- Limites já configurados: global 900 pedidos/min, Hub 120/min conservador, Metro até 1000/min, CM até 40/s; retries/redirecionamentos contam e Retry-After/429/503 mantêm-se. Não aumentar polling nem criar upstream requests por clique ou visitante.
- OpenAPI única → Go `oapi-codegen/v2` + cliente TS `oazapfts`; listas paginadas, chaves fornecidas com scopes obrigatórios e rate limits existentes.
- Cockroach/Postgres, retenção 30 dias e cap 5 GB sem alterações. Não adicionar arquivo histórico de itinerários, recalcular snapshots ou re-enriquecer posições antigas com um plano posterior.
- Recursos: container 1280 MiB, GOMEMLIMIT 768 MiB, GOMAXPROCS 2, gate local de RSS 1024 MiB. Último pior caso 999,1 MiB. Novas estruturas não podem multiplicar-se nas 64 revisões nem tornar o refresh estático inválido mais barato à custa de omitir validação.
- Agente principal executa planeamento, implementação, testes e correções. `quasar-alpha/xhigh` apenas revisões independentes, sem editar artefactos que revê. Sem novas bibliotecas, framework de navegação, registry de enrichers ou abstração genérica de dados.

## Vocabulário e identidade

**Veículo no mapa** é a entidade publicada pelo fornecedor, podendo ser uma unidade física ou estimativa de um serviço. **Número comercial** é `trip_short_name`/`service_label`; não é uma chave nem necessariamente o ID de uma unidade. **Viagem** precisa de operador, plano, source trip ID e dia operacional para ligações de horários/previsões. **Visita** é uma paragem numa sequência, podendo repetir a mesma estação. **Itinerário publicado** é ordem de paragens, sem ETA nem prova de movimento. **Previsão** é expectativa da fonte e não muda posição GPS. **Último registo** descreve apenas o estado na hora da observação retida.

Estas distinções orientam texto e matching. Não substituir IDs internos por nomes, extrair datas de sufixos de viagem, identificar veículos por distância ou ligar uma chegada ao primeiro veículo da linha.

## Apresentação proposta

### Cabeçalho e conteúdo de veículo

Ordem: ícone do modo + operador + número comercial verificado, destino publicado se disponível; botão de avisos com contagem e botão de fechar; estado factual compacto; hora e idade; percurso/paragens; características disponíveis; avisos e link da fonte.

Exemplo CP de último registo, quando há número/serviço, estado e paragem publicados:

> Comboio 18456 · Sintra → Lisboa Oriente · ⚠ Ver 2 avisos
>
> Último registo: parado em Lisboa Oriente · 13:06 · há 8 min
>
> Próximas paragens previstas …

A linha “Parado…” só é presente se o registo e a referência de paragem o suportarem. Se retido: “Último registo: parado em Lisboa Oriente”, ou apenas “Última posição conhecida” se o estado não foi publicado. Manter **Posição estimada** visível junto da identificação Metro; não esconder a natureza estimada só no fundo. Número comercial ausente não será substituído por um número inferido do ID.

- Eliminar a sucessão de caixas “Última posição…”, “Último estado…”, “Cobertura parcial…” no topo. Estado não é um aviso; mostrar uma vez, factual. “Parado” não é um erro.
- Avisos de idade/cobertura/matching/indisponibilidade ficam no fim, cada motivo no máximo uma vez. Mensagem agregada de disponibilidade não pode duplicar a frase mais específica.
- Exemplos inferiores: “Sem atualização há 8 min; mostramos o último registo.”; “Previsões CP com cobertura parcial.”; “Não foi possível associar previsões a este serviço.”; “A fonte não publica velocidade para estas posições estimadas.” Usar apenas os avisos aplicáveis; não rotular ausência de um atributo opcional como falha.
- Proveniência (“Fonte pública TML · CP”) e ressalva de previsão/horário são uma nota curta no rodapé, não uma caixa repetida por linha. Pode existir junto às horas a etiqueta curta “Previsão” ou “Planeado”.
- Botão de aviso acessível: `aria-label="Ver 2 avisos"`, título, ícone + contagem; sem depender apenas da cor. Clique faz scroll dentro do painel e foca o bloco inferior (`tabIndex=-1`), sem saltar toda a página. Respeitar prefers-reduced-motion. Não fazer auto-scroll a cada refresh.
- Fechar, Escape e clique fora mantêm o comportamento atual. Indicador de carga/erro junto à secção afetada; em refetch preservar conteúdo antigo claramente marcado, nunca lista branca.

### Campos opcionais e enriquecimento mínimo

Omitir o campo inteiro se null/undefined/empty/whitespace. Preservar `0` e booleanos: não usar `if(value)`. Especificações só têm grelha se houver valores. Não mostrar “Indisponível” em dez campos opcionais, nem legenda de especificações sem especificações.

| Campo | Texto curto | Regra |
|---|---|---|
| Route/serviço | Linha ou Carreira conforme modo | Nome comercial e número quando verificados; omitir se ausente. |
| Modelo/matrícula | Modelo; Matrícula | Só publicação por unidade identificada; preservar formatação portuguesa existente `CE-29-PV`. |
| Tipologia/propulsão | Tipologia; Propulsão | Traduzir somente enum textual conhecido da mesma fonte. Código numérico desconhecido mantém “Código publicado: …”; não aplicar tabelas de outra versão. |
| Capacidade | Lugares sentados; Capacidade total | Valores publicados, não lugares livres; zero é valor válido. Nunca calcular total a partir de uma ocupação. |
| Equipamento | Acessibilidade; Contactless | `true` publicado → Sim. `false` com semântica ambígua/default → “Não indicado pela fonte”, não prova de ausência. null → ocultar. Nota curta no rodapé só quando necessária. |
| Velocidade | Velocidade amostral | Só amostra corrente elegível; zero válido. Última posição não fornece velocidade atual. Metro estimado tem aviso inferior de capability, não campo vazio. |
| Posição | Reportada / Estimada | Badge visível; uma repetição na grelha é dispensável. |
| Observação | 13:06 · há 8 min | Sempre baseada na observação original. |

Enriquecimento candidato limitado a Mobi: aproveitar `available_seats` legado apenas quando a especificação dessa versão confirma capacidade, ID/agência/plano da unidade coincidem e valor inteiro está entre 0 e 10000. Metadados específicos atuais têm precedência; não overwriting com GTFS ausente. Testar joins em fixtures e fonte real já obtida. Se a semântica/qualidade falhar, omitir esse complemento, sem adiar limpeza UI.

Não incorporar `available_seats=1` Carris enquanto a qualidade estiver por confirmar. Não associar as unidades UT Metro ou 3501 Fertagus aos IDs live de serviços. Sem modelo ou capacidade CP/TTSL/TCB verificada, o campo desaparece. Não adicionar ocupação, lotação estimada ou fontes privadas.

### Nomes, linhas e idade

- CP, categorias exatas: AP → Alfa Pendular; IC → Intercidades; R → Regional; IR → InterRegional. Linhas Sintra, Cascais, Azambuja, Sado mantêm nomes publicados. Categoria desconhecida mantém o valor original; não substituir substrings em estações/destinos.
- Títulos CP consistentes: “Comboio 18456” quando `service_label` existe; linha/categoria e destino numa segunda linha curta. Horário e previsão usam o mesmo título, com etiqueta que distingue a origem temporal.
- Metro: “Linha Azul”, “Linha Amarela”, “Linha Verde”, “Linha Vermelha”, com swatch da cor GTFS. Proposta compacta: círculo com **Az/Am/Vd/Vm**, `aria-label` e tooltip com nome completo. Uma única letra faria Azul/Amarela e Verde/Vermelha indistinguíveis para quem não diferencia as cores. Não fundir os percursos publicados da Amarela por terem a mesma cor.
- Aplicar nomes a popup, chegadas, listas de carreira, pesquisa, legendas e tooltips relevantes; IDs e filtros não mudam. Destinos/paragens usam nomes do catálogo do mesmo plano, com trim/acentuação oficial já existente; sem title-case automático destrutivo ou crosswalk por nome de apresentação.
- Idade partilhada: <1 min “agora”, 1 min “há 1 min”, até 59 min “há X min”, depois “há 1 h”/“há X h”, com data/hora completas Lisboa em tooltip para observações de outro dia. Atualizar pelo relógio UI existente, não acrescentar polling.
- Timestamp inválido/futuro além da tolerância existente: não criar idade negativa nem corrigir hora da fonte; fallback textual e aviso se necessário. Testar segundos, fronteira de minuto, meia-noite e DST. Histórico continua associado à data consultada; não aplicar idade de posição atual indiscriminadamente a todos os gráficos.

## Mapa: ícones e dois alvos clicáveis

Usar BusFront, Ship, TramFront e TrainFront da Lucide instalada, como desenhos estáticos locais reutilizados nos sprites. Ring do operador e diferenciação de estimativa/parado/antigo mantêm-se. Não usar imagens externas por veículo, HTML de fornecedores nem nova dependência de ícones.

A geometria reportada não se move. Quando um ícone de veículo cobre um alvo de estação visível, calcular um deslocamento em **píxeis do ecrã** e desenhar uma ligação discreta ao ponto original. Coincidência visual não confirma que o veículo está na estação. Aplicar a todos os modos, atuais e retidos.

1. Projetar só alvos visíveis para uma grelha espacial; incluir tamanho do ícone e alvo da estação, não apenas igualdade de coordenadas. Dedupe círculo CP e círculo base pela mesma paragem. Evitar O(todos os veículos × todas as paragens).
2. Fixar estação na coordenada original; atribuir posições estáveis aos veículos por ID em torno dela. Separação proposta de pelo menos 48 CSS px entre centros interativos, com alvo de toque de 44 px e teste de que zonas disjuntas existem para ambos. Converter corretamente `icon-offset` à escala do sprite; alinhamento ao viewport.
3. Limitar a oito slots por grupo visual. Acima disso, abrir seletor de alvos com estação e todos os veículos daquele grupo, lista scrollável e sem truncação silenciosa. Em grupos já densos de estações/veículos, preservar todos os alvos nesse seletor em vez de escolher o primeiro. Limite visual não limita os dados subjacentes.
4. Recalcular em zoom/pan/rotação/resize, alteração de seleção/layers e atualização dos dados, com trabalho coalescido por frame. Com estações desligadas, remover os offsets que dependem delas. A navegação centra na coordenada original, nunca no offset.
5. Seleção usa todos os alvos do ponto/zona de toque, dedupe por tipo+ID. No caso simples, clicar cada ícone abre o respetivo popup; caso ambíguo abre escolha explícita. Testar DPR 1/2, desktop/mobile e marker com opacidade baixa.
6. Acesso por teclado através das listas de veículos/paragens e do seletor DOM; não transformar milhares de features em botões HTML. Estado selecionado e foco visíveis; ícones têm legenda/nomes por modo.

Os offsets/conectores são estado de apresentação: não persistem na API, SQL, snapshots, distância, velocidade ou previsão.

## Navegação e próximas paragens

### Associação da estação ao veículo

Associar chegadas/previsões e veículos no backend, usando a mesma revisão de cache, operador + plano + source trip ID + dia operacional; só expor ligação quando há exatamente uma entidade compatível e ainda retida. Identidade de CP não é o `Arrival.trip_id` datado gerado; usar `source_trip_id` publicado.

- A linha inteira pode ser botão quando há link verificado; nome/número/destino/horas continuam legíveis. Mostrar “Ver veículo” e natureza estimada/último registo quando aplicável.
- Sem correspondência ou com múltiplos candidatos: linha continua informativa, sem botão enganador; explicação curta inferior por secção. Planeado não prova veículo ativo.
- Além das chegadas, mostrar “Veículos com referência a esta paragem”: acrescentar `stop_id?` ao endpoint paginado existente de veículos e aplicar o filtro que hoje o handler ignora. Validar paragem do catálogo/operador selecionado; incluir filhos apenas pela relação parent_station publicada, rejeitando desconhecida ou incompatível. A resposta projeta entidades correntes/retidas da mesma revisão e fornece referências congeladas, sem segundo endpoint novo nem fetch upstream. Usar `stop_id` normalizado e estado publicado: “Parado”, “A chegar”, ou “Referência publicada” se o estado falta. Retidos aparecem como “Último registo”, separados de presença atual. Não criar ETA ou métricas a partir da referência.
- Agregar plataformas através da relação parent_station publicada, mantendo a visita/plataforma correta; não fundir estações com nome igual.
- API direta Metro: o `comboio` da espera não corresponde comprovadamente ao ID estimado Hub; esses itens continuam sem salto ao veículo. Nunca remover zeros/letras nem escolher por linha para inventar essa correspondência. A secção de referências Hub da estação pode ser clicável quando o stop crosswalk for único.

### Do veículo às paragens

- CP: previsões correntes já associadas ao serviço exato primeiro; complementar visitas planeadas sem duplicar a mesma visita. Ordem por stop_sequence, não por nome. Previsão expirada não reaparece como corrente nem desloca GPS.
- Carris/Mobi/TCB/TTSL/Fertagus: reutilizar o horário do serviço exato e dia operacional. Sem ETA publicada, indicar “Horário planeado”, nunca “Chega em X min”.
- Metro: crosswalk da referência de estação só por código/catálogo oficial e correspondência de nome completo normalizado, corroborada por coordenadas e relação de plataforma/parent publicada; sem match por prefixo, “primeiro nome próximo” ou colisões. Invalidar por plano. Percurso planeado do trip/plano/dia identificado, rotulado “Serviço estimado · percurso planeado”; referências de estação só se crosswalk oficial único. Isto não confirma unidade física nem associa esperas da API direta.
- CM: próxima paragem explicitamente reportada disponível como fallback; completar ordem do percurso por trip/plano/agência exatos a partir dos GTFS que já recebemos, se passar o gate de recursos abaixo. Sem dia confirmado, esse percurso é **ordem publicada**, sem calendário ou horas.
- Estado STOPPED_AT + referência única identifica visita atual; INCOMING_AT/IN_TRANSIT_TO referencia destino próximo. Se paragem aparece mais de uma vez e não há sequência publicada, progresso é desconhecido. Não escolher a primeira ocorrência, projetar GPS na linha ou usar apenas o relógio para eliminar paragens atrasadas.
- Com progresso desconhecido: título “Percurso planeado na área de Lisboa” e preservar a ordem das visitas retidas da viagem, em páginas, sem chamar a todas as linhas “próximas”. O parser já exclui visitas fora da área regional (`gtfs_parser.go:108–110,177–178`): não existe aqui a sequência nacional completa. Resposta e UI incluem cobertura `regional_subset` e nota inferior “Mostramos as paragens publicadas disponíveis na área de Lisboa.” Ausência de mais visitas regionais não prova fim do serviço. Os extremos completos CP, retidos separadamente, continuam informativos, sem tornar as estações exteriores clicáveis ou inventar visitas. Com retido: “Percurso do último serviço observado”, original timestamp, sem afirmar que ainda segue esse percurso. Previsões futuras CP são “Próximas paragens previstas”, não confirmação de presença.
- Cada paragem válida é botão com nome legível; resposta contém o Stop normalizado (incluindo coordenadas) para evitar um pedido por linha. Stop ausente é texto sem link e gera uma ressalva única de cobertura.
- Foco abre o popup correto, centra no ponto original e fecha a escolha anterior. Reutilizar handlers simples `selectVehicle`/`chooseStop`, sem sistema novo de navegação. Se o filtro de carreira atual excluir o alvo, limpar só esse filtro e mostrar aviso curto “Filtro de carreira removido”; conservar operadores e overlays. Target de outra plataforma usa dados do mesmo plano, sem adivinhar.
- Só efetivar navegação após obter o alvo na revisão e no serviço exatos da ligação. Referência derivada inclui revisão/asOf e identidade de serviço; um ID de veículo que mudou de viagem não é suficiente para validar o salto. Se a revisão foi removida ou expirou, atualizar a secção de origem e voltar a obter uma ligação ao mesmo serviço exato, única; sem isso mostrar “Esta ligação expirou” e preservar o popup. Nunca tentar apenas o vehicle_id na revisão mais recente. Durante carga preservar popup de origem; erro/expiração mostra botão de tentar novamente, sem fechar para uma lista vazia. Deseleção do operador elimina alvo e chamadas pendentes; pedido antigo não reabre popup.

### Alterações mínimas no contrato

1. `service_label?` em Arrival e Trip; em Vehicle só quando capturado do serviço/plano/dia exatos. Não mudar IDs. `vehicle_ref?` em Arrival e CpPrediction quando associação única; em Vehicle projetado para navegação no mapa/lista e na secção da paragem. Tipo mínimo `{vehicle_id, reference}`: reference é token opaco de navegação (até2048bytes), distinto dos cursores endpoint-specific `t:`/`v:` existentes. Liga cache revision + asOf + operator/id + observed_at + tuple exata plan/source_trip/operational_date quando disponível; cada campo é validado contra a entidade dessa revisão no servidor, não é uma prova de autorização. Se não existe tuple completa, só navega à entidade publicada e ao fallback compatível, sem join de horário. Referências calculadas para cópias de resposta, nunca gravadas em histórico/cache de observações/snapshots de previsões originais ou hashes/relógios de origem.
2. **Um endpoint** `GET /api/v1/vehicles/{vehicle_id}/calls`: recebe `reference?` para navegação e resposta contém a entidade selecionada/projetada, serviço opcional, disponibilidade, cobertura regional explícita, progresso conhecido/desconhecido, `data` de visitas e `page`. A reference precisa de apontar ao mesmo vehicle_id do path e à identidade congelada; o handler não a interpreta como cursor de outro endpoint. Sem reference, clientes API podem pedir a entidade mais recente, mas o UI usa sempre a reference do alvo clicado. A entidade incluída também permite abrir um alvo fora da primeira página/filtro da lista de veículos, sem criar segundo endpoint de detalhe.
3. Visita: Stop do catálogo ou referência ausente explícita, stop_sequence opcional, kind (`predicted`, `scheduled`, `published_route`), horas opcionais e fonte/atualização/desvio quando realmente publicados. Não inventar horas para uma rota sem calendário; identificação de disponibilidade/reason finita, sem mensagens upstream arbitrárias.
4. `limit` default20/max500, `offset` e `revision` coerentes com contrato atual. Revisão de calls inclui cache+asOf da projeção, identidade de serviço, versões das fontes e expiry; fixa entidade/plano/visitas/ordem entre páginas. Quando contém previsões CP, reutilizar regra de `cp_reads.go:38–47`: o resultado inteiro expira no menor ValidUntil das previsões incluídas, mesmo que a cache revision ainda exista. Expiry verifica-se também na primeira página e antes de devolver páginas seguintes; nunca filtrar previsões por página, porque mudaria membership. 410 expira todo o resultado; UI reinicia a primeira página uma vez, com nova reference validada para o mesmo serviço. Se previsões já expiraram, devolver fallback planeado regional fresco/claramente rotulado ou disponibilidade, nunca ressuscitar a previsão nem saltar para o novo serviço do mesmo ID. 404 para ID desconhecido/expirado >1h, 400 para filtros/revisão inválidos, 429/503 como hoje. IDs opacos validados até256bytes e URL-encoded; tratar `[plano][agência]` como publicação que requer validação explícita contra planos existentes, não como input confiável livre.
5. Dados presentes mas sem associação segura → 200 com disponibilidade e lista vazia/fallback; não 500. Planos divergentes não re-enriquecem a posição antiga: devolvem motivo de mismatch. Atenção ao helper existente `projectVehicle` (`continuity_publication.go:143`), que hoje chama `enrichVehicle` e pode completar metadados pelo StaticData atual (`ingest.go:530–543`): o novo handler/projeção de calls tem de preservar a entidade da revisão/observação e só usar plano comprovadamente igual para enriquecimento. Não invocar o helper de forma que aplique um plano posterior; teste específico de modelo/paragem/itinerário alterados no refresh. Sem projeto separado de limpeza geral. Chamadas CP já podem reutilizar o cache de previsões existente.
6. `read:transit`, público sem chave hoje, scopes de chaves fornecidas impostos. Incluir novo path na classificação de expensive reads (dois slots,15s), cancelamento durante loops, limites de resultados e no upstream no handler. Nunca ampliar janela global só para mostrar um percurso.
7. Gerar Go e TS da mesma spec; usar cliente gerado em todos os fluxos. Componentes não constroem URLs/fetches paralelos à spec. Não gravar payload de itinerary na observação ou em cada revisão de veículos. Acrescentar optional stop_id ao ListVehicles na mesma spec, com filtro por StopId/parent publicado e reference congelada por linha; não assumir que o parâmetro já funciona apenas porque Filter o contém.

### CM: gate de dimensão antes de prometer percursos completos

O índice mínimo relaciona **trip exato dentro do plano/agência** a uma lista ordenada deduplicada de visitas; não retém todos os stop_times/horas/calendários nem escolhe a primeira versão de pattern. Construir em streaming nos quatro archives já recebidos; conservar identificação de variantes diferente quando a sequência/headsign/shape difere. Uma versão futura não pode substituir a sequência da viagem publicada.

Primeiro medir contagem real de versões, referências e bytes incrementais de estrutura/cache serializado, incluindo refresh simultâneo e revisões sobrepostas. Reutilizar a estrutura estática imutável por revisão; sem copiar o índice por posição. Preservar budget CSV/ZIP/CRC e validação já existente.

Gate obrigatório: workload de oito operadores com 64 revisões, backlog20000,1000entidades/operador/tick, refresh completo sobreposto,64snapshots CP perto do cap e dois decodes de16MiB, nas mesmas configurações aceites; pico RSS ≤1024MiB. Executar leitura paginada concorrente e cancelamento. Não diminuir a fixture, dados ou cap de retenção para passar.

Se falhar: não publicar o índice completo; entregar a próxima paragem reportada CM, clickable e rotulada, com aviso único “Percurso completo indisponível; mostramos a próxima paragem publicada”. Reportar a limitação na aceitação. Não abrir um novo esforço de otimização geral, banco de percursos ou fetch on-demand upstream nesta mudança. Esta alternativa está definida antes de executar e não promete funcionalidades sem recursos demonstrados.

## Sequência de implementação proposta

| Etapa | Trabalho pelo agente principal | Prova para seguir |
|---|---|---|
| 1. Contrato e matching | Propagar número já lido, schemas opcionais, DTO visits, endpoint cache-only e associação única; revisão/paginação/cancelamento. | Fixtures de identidades positivas/negativas, geração Go/TS sem drift, scopes/rate-limit/404/410/503. |
| 2. Dados úteis e limites | Mobi capacity condicional e crosswalk Metro oficial bidirecional único com invalidação por plano; medir índice exato CM antes de reter. | Zero/defaults/precedência, ambiguidades fail closed, resource gate sem enfraquecer. |
| 3. Textos e popups | Nomes CP/Metro, idade, campos opcionais, aviso/footer, skeleton/carga/refetch, títulos de serviço consistentes. | Tests de valores/clock; browser keyboard/mobile/outside/Escape/warnings. |
| 4. Navegação | Botões das chegadas, referências à estação e visitas do veículo, alvo/revisão/erro/filtro/seleção. | Round-trip estação→veículo→paragem nos oito providers, com expected unavailable explícito para joins impossíveis. |
| 5. Mapa | Sprites por modo, grelha/offsets/conectores e seletor para múltiplos alvos. | Browser no MapLibre real, clicks/touch independentes, zoom/bearing/DPR/states; coordenadas e métricas invariantes. |
| 6. Aceitação | Testes completos relevantes, normal Maat, revisão final quasar-alpha/xhigh e correção dos blockers pelo principal. | Revisor recomenda aceitação e principal confirma requisitos + limitações. Deploy só depois de pedido/autorização para executar. |

Ficheiros previstos: `api/openapi.yaml` e derivados gerados; `internal/app/{gtfs.go,vehicle_service.go,server.go,cp_reads.go,metro.go,metadata.go,gtfs_shapes.go}` onde necessário, mais pequeno handler/cache itinerary se separar tornar código mais legível; `frontend/src/{App.tsx,Map.tsx,CPPredictions.tsx,VehicleSpecifications.tsx,data.ts,style.css}` . Tests junto aos públicos existentes. Não introduzir interfaces/serviços genéricos ou dividir módulos sem benefício concreto.

## Matriz de testes e aceitação

- **Número CP:** 2226 labels disponíveis no plano observado; fixture planeado/previsão mesmo trip produz mesmo número/título. Sem label não extrai source ID. Categoria AP/IC/R/IR e desconhecida; headsign/nomes com substrings não são alterados.
- **Identidade:** mesmo trip noutro operador/plano/dia, ausência de dia, duas entidades Metro, loops sem sequência, plano expirado/refresh, parent/platform e paragens homónimas → sem join inventado. Last-known não adquire novo modelo, nome ou trajeto de plano posterior. Testar reference congelada cujo mesmo vehicle_id mudou trip/plano/dia, eviction, mismatch com path, referência adulterada, retry que só volta a ligar se existir o mesmo serviço, e paginação calls em duas páginas cruzando o menor ValidUntil CP (410 global, sem membership alterado). Testar 24h+/DST/dia operacional quando existem horas planeadas.
- **Oito providers:** fixture por operador cobre correspondência possível e ausência de dados; CM variante atual/futura e IDs de agências; filtro ListVehicles stop_id inclui só referências à paragem/plataformas publicadas na revisão, rejeita desconhecida e respeita operadores, route e paginação; Metro códigos bidirecionais únicos, ambiguidades e direct wait que permanece sem vínculo; CP previsões válidas/parciais/expiradas e serviço sem previsões; fixture CP exterior→interior→exterior mantém extremos nacionais publicados mas só visitas regionais clicáveis, coverage explícita e sem falso “serviço terminado”; Fertagus/Metro unidade física diferente não ganha specs.
- **Campos:** nenhum “Indisponível” em grelha de campos opcionais sem valor; strings vazias/whitespace,0,false/null, boolean default ambíguo, velocidade0/corrente/retida/unsupported, fonte/código desconhecido, matrículas já formatadas. Mobi precedência e identidade exata; Carris1/1 não adicionado.
- **Relógio:** agora/1/59/60min, timestamp futuro/inválido, hora Lisboa/DST/mudança de data; refetch/retido não muda idade. Estado STOPPED válido sem atualização não passa a movimento; ausência de estado não vira parado.
- **Avisos:** cada motivo uma vez; no fundo; warning icon chega e foca footer, não rola página; estimativa visível no topo; quantidade correta; close/outside/Escape continuam; carregamento, erro e retry, sem reabrir popup após deseleção.
- **Navegação:** chegadas/visitas clicáveis no mouse/teclado/touch, salto fora da lista/filtro, preservação de operador/overlay, limpeza do filtro anunciada, alvo removido/410 e resposta fora de ordem. Não considerar indisponibilidade verdadeira como regressão ou esconder com mock de join inventado.
- **Mapa real:** um comboio por cima de estação, oito veículos e overflow, station layer off, círculo CP duplicado, outras estações próximas, todas as modalidades, atual/estimado/parado/retido, zoom/pan/bearing/resize/DPR1/2. Ambos os alvos individualmente clicáveis ou seletor com todos. Coordenadas, observed_at, distância/speed/history inalterados.
- **API/security/resource:** cliente/server gerados, paginação sem duplicados e congelada, scoped key negativa, rate limits e no fetch upstream, resultado/cancelamento/deadline/concurrency, ID malformado. Go race com Postgres/Cockroach locais quando backend mudado; restore/cache warm-start e resource workload com estruturas novas.
- **Browser visual:** desktop e mobile reais, painel comprido scrollável sem overflow, contraste de Amarela/swatch, abreviaturas diferenciáveis sem cores, warnings/calls não cobrem X. Suite existente mais regressões significativas; testes não só snapshots do markup.
- **Maat/revisões:** gate normal sem bypass e sem desativar assinatura; resolver issues relevantes e rerun após alterações. Entregar ao revisor plano, diff, limitações e resultados. Revisão final independente até recomendação de aceitação; decisão final do principal.

## O que pode continuar indisponível depois desta mudança

Modelos e capacidades sem unidade identificada, salto espera direta Metro→entidade Hub sem crosswalk publicado, sequência restante de loops sem progresso e percurso CM completo se exceder memória. Nestes casos a interface omite atributos, conserva dados úteis e explica a limitação uma vez. A mudança não garante cobertura que os fornecedores não publicam.

Sem novo login, novas análises, rota calculada, promessa de GPS na estação, novo feed privado, aumento de polling, armazenamento histórico adicional ou deploy nesta etapa.


## Resultado da execução e decisões condicionais

O índice completo CM foi medido em dados oficiais: 266184 viagens,1621 variantes exatas e57286 referências ordenadas. O candidato preserva trip/plano/agência e deduplica apenas sequências equivalentes; não escolhe uma primeira viagem por linha. Com refresh sobreposto, o candidato medido alcançou1117274112 bytes de RSS (~1065,5MiB), acima do gate1024MiB. Por isso não se publica esse índice: CM oferece a próxima paragem explicitamente reportada, sem ETA inventada, com o aviso do fallback aprovado. O código candidato existe apenas na fixture opt-in de medição.

A revisão exigente da memória revelou que a versão exata inicial já ultrapassava o gate com os snapshots/decodes CP incluídos. A correção estreita conserva todas as visitas/horários: elimina capacidade não utilizada dos slices e armazena relógios GTFS de até72h em int32; stop_sequence continua int. Cache JSON legado continua suportado e overflow é rejeitado. Nenhum cap, fixture, retenção ou limite de pedidos foi reduzido.

A revisão independente identificou e o principal corrigiu relógios finais/expiry, consistência de observações e percursos, idade das referências de estação, validação do catálogo antes de abrir uma paragem, agência Mobi e foco do seletor. A proveniência stop_plan_id/stop_static_updated_at foi acrescentada à mesma spec para verificar saltos; não é guardada em snapshots. Catálogo de paragens e operador são lidos na mesma revisão. Ausência de plano CM é normalizada em ambos os lados; identidade e timestamp continuam obrigatórios.

A aceitação local e as limitações estão registadas em [VALIDATION](research/popup-map-navigation/VALIDATION.md). Nenhuma alteração foi publicada nesta etapa.
