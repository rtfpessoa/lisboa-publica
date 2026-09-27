# Próximas passagens e navegação de paragens

Implementado em `rodrigo/fix-carris-metropolitana`. Esta validação é local; não representa publicação em produção.

A Carris Metropolitana importava linhas e paragens, mas não recolhia chegadas nem tinha horários no `Schedule`. O endpoint devolvia sucesso com uma lista vazia. Carris, TCB, MobiCascais, TTSL e Fertagus tinham GTFS planeado, sem integração das previsões TML. CP e Metro já tinham integrações próprias.

## Comportamento

- CM: consultar apenas a paragem solicitada na API oficial `/v2/arrivals/by_stop/{id}`; conservar horários válidos se falhar a previsão e distinguir loading, lista publicada vazia, erro, dados antigos e cobertura parcial.
- Cinco operadores TML: usar o mesmo pedido ETA existente de CP, com tradução reversível de IDs por plano/agência/viagem exata e sequência única. Conflitos, frequência sem descritores suficientes e instâncias ambíguas não geram associações inventadas.
- A hora absoluta publicada prevalece sobre o atraso. A inferência de dia requer prova única; `date_basis` distingue data publicada de data inferida do horário. Desacordo mantém a ETA útil sem campos de instância não demonstrados. Veículo só com identidade, plano, viagem, data operacional e observação compatíveis.
- Janela padrão de chegadas: uma hora. Ordenação pela previsão e deduplicação por instância/visita demonstrada. Páginas de previsões ficam fixas; expiração, expulsão, reinício ou mudança da geração estática devolve 410. A ligação ao veículo é revalidada no pedido inicial e na interface quando muda a viagem GPS; isso não apaga a ETA. Páginas apenas GTFS conservam o comportamento de revisão anterior.
- Popup: grupo até 50 m da primeira paragem, com ordem estável durante troca e zoom; anterior/seguinte, setas esquerda/direita com foco, Tab e Escape. Zoom e arrasto do mapa conservam o grupo; horas já passadas ou expiradas saem da lista mesmo se falhar uma atualização. Operadores ocultos ficam excluídos. Pontos sobrepostos oferecem veículos e paragens como alvos explícitos.

## Limites finais

32 seleções TML e 32 CM, interesse de 30 s, sem prefetch das alternativas. CM: dois pedidos em voo, mínimo de 5 s por chave, máximo de 48 admissões/minuto e resposta até 512 KiB. O transporte global mantém limites, cooldown e TLS existentes.

Até 256 linhas/256 KiB normalizados por seleção. Retenção contabilizada: 3 MiB TML, 1 MiB CM e 1 MiB de resultados paginados, até 64 tokens. Duas leituras caras em voo mantêm o limite existente; o ensaio inclui leitores de resultados já expulsos. A contabilidade usa margem para estruturas, strings e arrays; não corresponde apenas ao tamanho JSON. Tokens e seletores ficam limitados e não conservam o backing string do URL do pedido.

O workspace TML conserva no máximo 512 candidatos e 4096 nós da tradução, incluindo ambas as direções. Não existem índices expandidos de todas as viagens dos cinco operadores nem coleções ETA nas 64 revisões de `State`. Tempos nacionais necessários à validação usam um escalar compacto por viagem; CP conserva o formato e as regras anteriores.

## Evidência

- `go test -race ./...` com PostgreSQL local: passou, incluindo os endpoints HTTP reais, cinco fixtures de identificadores capturados, falhas, clocks originais, conflitos, visitas repetidas, associação única ao veículo, paginação, cache cheio, concorrência/cancelamento, horários além de 24 h, mudança de ano e ambas as transições DST. [Bateria completa](research/stop-arrivals/validation/go-race.txt) e [regressões finais de chegadas](research/stop-arrivals/validation/arrivals-final.txt).
- Browser: 27 testes passaram (desktop/mobile, sobreposições, CP, continuidade e estado de serviço). Dois ensaios opcionais de geometria completa não foram executados nesta bateria. [Bateria de regressões](research/stop-arrivals/validation/browser.txt), [navegação final](research/stop-arrivals/validation/stop-navigation-final.txt) e [CP/popup final](research/stop-arrivals/validation/browser-final.txt).
- Build TypeScript/Vite, `go vet ./...`, geração OpenAPI e `git diff --check`: passaram.
- Gate combinado com oito redes oficiais completas, 64 revisões, 1000 veículos por operador/tick, 20 000 observações pendentes, refresh estático, 64 snapshots CP e duas decodificações de quase 16 MiB: mantido. Acrescentámos saturação de ETA/cache/tokens, dois leitores antigos e workspace/respostas CM. Pico RSS **1001,58 MiB**, abaixo de **1024 MiB**, com `GOMEMLIMIT=768MiB`. [Registo final](research/stop-arrivals/validation/resources-final.txt).

O desenho inicial atingiu 1033,89 MiB e foi rejeitado. Compactámos os metadados e reduzimos os caps novos; não aumentámos o gate nem reduzimos o workload anterior. Os [registos inicial](research/stop-arrivals/validation/resources.txt) e [compacto](research/stop-arrivals/validation/resources-compact.txt) ficam conservados.

As fixtures TML conservam os IDs oficiais e a proveniência da captura. Os testes HTTP rebaseiam apenas os tempos/calendário para não transformar uma captura antiga em observação atual. A fixture TTSL antiga não é evidência de previsões futuras disponíveis hoje; a interface mantém o fallback quando a fonte não as publica utilizáveis. A CM não publica um relógio de atualização nesta resposta: mostramos uma previsão publicada com prazo de recolha limitado, sem inventar `source_updated_at` ou veículo.

## Correções do gate Maat

A recolha CM, tradução/visitas TML, prova da data, seleção imutável, associação ao veículo e composição HTTP foram separadas por responsabilidade. O transporte, ciclos de recolha e ligação das viagens GTFS têm módulos próprios; limites de retenção e aquisição têm nomes explícitos. Nenhum cap, relógio, workload ou threshold foi relaxado.

Após a extração, a bateria completa PostgreSQL com race detector passou novamente; vet, build e geração determinística também passaram. O ensaio de redes completas foi repetido com a mesma carga. [Race após Maat](research/stop-arrivals/validation/post-maat-race.txt) e [recursos após Maat](research/stop-arrivals/validation/post-maat-resource.txt).

O commit final passa o gate normal Maat no âmbito Go da aplicação: **87/delta0**, zero regressões críticas e zero supressões. TypeScript é validado separadamente; não se afirma cumprimento do threshold absoluto95. [Veredito do hook](research/stop-arrivals/validation/maat.json). O novo pico do ensaio é **1002,09 MiB**, inferior a1024MiB.
