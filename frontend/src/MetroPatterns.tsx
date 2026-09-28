import {useMemo,useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import * as api from './api';
import {allPages,errorText,number,time} from './data';

const unavailable:Record<string,string>={
 official_anchor_only:'Âncora oficial; a nossa previsão começa nos alvos seguintes',
 association_not_supported:'Associação entre estações ainda insuficiente',
 missing_fresh_future_anchor:'Sem âncora oficial futura recente',
 estimated_arrival_already_past:'Estimativa anterior ao momento atual; sem suporte para atualizar o progresso',
 component_summary_limit:'Resumo limitado pela capacidade disponível',
 archive_unavailable:'Histórico experimental indisponível',
 source_expired:'Fonte ou associação sem suporte recente',
 insufficient_compatible_components:'Ainda sem histórico compatível para o percurso restante',
};

const operators = {metro:'Metro de Lisboa',cm:'Carris Metropolitana',carris:'Carris',cp:'CP',fertagus:'Fertagus',ttsl:'Transtejo / Soflusa',tcb:'TCB',mobi:'Mobi Cascais'} as const;
type Operator = keyof typeof operators;

export default function MetroPatterns({initialStop}:{initialStop?:string}){
 const [operator,setOperator]=useState<Operator>(initialStop&&initialStop.split(':')[0] in operators?initialStop.split(':')[0] as Operator:'metro');
 const [station,setStation]=useState(initialStop??'');
 const [dayType,setDayType]=useState('');
 const [direction,setDirection]=useState('');
 const [route,setRoute]=useState('');
 const [journey,setJourney]=useState('');
 const stops=useQuery({queryKey:['pattern-stops',operator],queryFn:()=>allPages(p=>api.listStops({...p,operators:operator})),staleTime:300000});
 const stopId=station||stops.data?.[0]?.id;
 const query=useQuery({queryKey:['metro-patterns',operator,stopId],queryFn:({signal})=>operator==='metro'?api.getMetroPatterns({stopId},{signal}):api.getTransportPatterns(operator,{stopId},{signal}),refetchInterval:30000});
 const data=query.data;
 const routes=useMemo(()=>[...new Set([...(data?.patterns??[]).map(p=>p.route),...(data?.forecasts??[]).map(f=>f.route),...(data?.evaluation??[]).map(r=>r.route)])].sort(),[data]);
 const selectedRoute=routes.includes(route)?route:routes[0];
 const directions=useMemo(()=>[...new Set([...(data?.patterns??[]).filter(p=>p.route===selectedRoute).map(p=>p.direction),...(data?.forecasts??[]).filter(f=>f.route===selectedRoute).map(f=>f.direction),...(data?.evaluation??[]).filter(r=>r.route===selectedRoute).map(r=>r.direction)])].sort(),[data,selectedRoute]);
 const selectedDirection=directions.includes(direction)?direction:directions[0];
 const selectedDayType=dayType||data?.current_day_type||'weekday';
 const patterns=(data?.patterns??[]).filter(p=>p.route===selectedRoute&&p.day_type===selectedDayType&&p.direction===selectedDirection);
 const waiting=(data?.forecasts??[]).filter(f=>f.route===selectedRoute&&f.function==='waiting'&&f.direction===selectedDirection);
 const independentOfficial=(data?.forecasts??[]).filter(f=>f.route===selectedRoute&&f.function==='waiting'&&f.direction==='unassociated'&&selectedDirection!=='unassociated');
 const journeys=[...new Set(waiting.filter(f=>f.episode).map(f=>f.episode))];
 const selectedJourney=journeys.includes(journey)?journey:journeys[0];
 const following=useQuery({queryKey:['metro-pattern-journey',operator,stopId,selectedJourney],queryFn:async({signal})=>{
  // A station query includes that journey's remaining calls through a bounded
  // dedicated filter, preserving the values issued by the collector.
  return operator==='metro'?api.getMetroPatterns({stopId,episode:selectedJourney},{signal}):api.getTransportPatterns(operator,{stopId,episode:selectedJourney},{signal});
 },enabled:!!selectedJourney,refetchInterval:30000});
 const ownDays=Math.min(...waiting.flatMap(f=>f.components.map(c=>c.days)));
 return <main className="page-panel transport-patterns"><section className="card"><h2>{operator==='metro'?'Padrões e previsões do Metro':`Padrões e previsões · ${operators[operator]}`}</h2><p className="notice">Experimental · {operator==='metro'?'Os sinais são inferidos das previsões do operador.':'Os sinais usam transições de estados publicados nas paragens e posições comunicadas.'} Não confirmam passagens físicas nem a identidade física do veículo.</p><div className="table-toolbar"><label>Operador <select aria-label="Operador dos padrões" value={operator} onChange={e=>{setOperator(e.target.value as Operator);setStation('');setJourney('');setDirection('');setRoute('')}}>{Object.entries(operators).map(([id,name])=><option key={id} value={id}>{name}</option>)}</select></label><label>Estação / paragem <select aria-label="Estação dos padrões" value={stopId??''} onChange={e=>{setStation(e.target.value);setJourney('');setDirection('');setRoute('')}}>{stops.data?.map(s=><option key={s.id} value={s.id}>{s.name}</option>)}</select></label><label>Rota <select aria-label="Rota dos padrões" value={selectedRoute??''} onChange={e=>{setRoute(e.target.value);setDirection('');setJourney('')}}>{routes.map(id=><option key={id} value={id}>{id}</option>)}</select></label><label>Tipo de dia <select value={selectedDayType} onChange={e=>setDayType(e.target.value)}><option value="weekday">Dias úteis</option><option value="saturday">Sábado</option><option value="sunday">Domingo</option><option value="holiday">Feriado (calendário de Lisboa)</option><option value="calendar_unknown">Calendário desconhecido</option></select></label><label>Destino <select value={selectedDirection??''} onChange={e=>{setDirection(e.target.value);setJourney('')}}>{directions.map(d=><option key={d} value={d}>{waiting.find(f=>f.direction===d)?.destination_name||`Destino publicado ${d}`}</option>)}</select></label></div>
 {query.isPending&&<p role="status">A carregar os dados recolhidos…</p>}{query.error&&<p className="error">{errorText(query.error)}</p>}{stops.error&&<p className="error">{errorText(stops.error)}</p>}
 {data&&<><p>{data.message}</p>{data.status!=='collecting'&&<p className="notice">Estado da recolha: {data.status}. Os intervalos podem conter lacunas.</p>}<p className="subtle">{data.collected_days} dias com dados admissíveis nesta estação · {data.gaps} interrupções conhecidas · última recolha {time(data.as_of)} · detalhe {data.detail_days} dias · agregados até {data.aggregate_months} meses, sujeitos ao espaço disponível.</p>{data.collected_days<30&&<p className="notice">Poucos dados: os valores disponíveis podem ser pouco precisos.</p>}
 <div className="hour-pattern-grid" aria-label="Sinais inferidos por hora">{Array.from({length:24},(_,hour)=>{const rows=patterns.filter(p=>p.hour===hour&&!p.component_target);const count=rows.reduce((n,p)=>n+p.signals,0);return <div key={hour} className={count?'hour-has-data':''}><strong>{String(hour).padStart(2,'0')}h</strong><span>{rows.length?number(count,0):'—'}</span><small>{rows.length?'sinais inferidos':'sem sinais sustentados'}</small></div>})}</div>
 <p className="subtle">A ausência de sinais não prova ausência de serviço. Probabilidade física e faixa Wilson de 95% indisponíveis: falta um denominador de dias com deteção sustentada. Tempo parado e velocidade indisponíveis.</p>
 <details><summary>Componentes por hora, visita e perfil</summary><div className="table-wrap"><table><thead><tr><th>Hora</th><th>{operator==='metro'?'Cais':'Visita publicada'}</th><th>Componente chegada→chegada</th><th>Média modelada</th><th>Amostras / dias</th><th>Condição / resolução</th></tr></thead><tbody>{patterns.filter(p=>p.component_target).map((p,i)=><tr key={i}><td>{p.hour}h (UTC{p.offset_seconds>=0?'+':''}{p.offset_seconds/3600})</td><td>{p.platform}</td><td>{stops.data?.find(s=>s.id===p.component_target||s.id===operator+':'+p.component_target||s.source_id===p.component_target)?.name??p.component_target}</td><td>{p.mean_component_seconds==null?'Indisponível':number(p.mean_component_seconds/60)+' min'}</td><td>{p.component_samples} / {p.days}</td><td>{p.condition==='reported_normal'?'Normalidade comunicada':p.condition==='reported_disruption'?'Perturbação comunicada':'Desconhecida'} · {p.resolution_seconds>0?p.resolution_seconds+'s':'Resolução combinada indisponível'}</td></tr>)}</tbody></table></div></details>
 </>}
 {data&&data.operators.length>0&&<details><summary>Recolha por operador</summary><ul>{data.operators.map(o=><li key={o.operator}>{o.operator.toUpperCase()}: {o.enabled?o.status==='waiting'?'a aguardar amostras':'recolha experimental':'etapa ainda desativada'} · {o.forecasts?'previsões experimentais':'previsões próprias ainda indisponíveis'}{o.as_of&&` · última amostra ${time(o.as_of)}`}</li>)}</ul><p className="subtle">Recolher posições publicadas não confirma chegadas, partidas ou passagens físicas.</p></details>}
 {data&&<EvaluationTable rows={data.evaluation.filter(r=>r.route===selectedRoute&&(!selectedDirection||r.direction===selectedDirection))}/>}
 </section><aside className="card"><h3>Próximo serviço nesta estação</h3><p className="subtle">Oficial e nossa previsão aparecem lado a lado. A nossa usa componentes do percurso restante, com âncora oficial quando existe; a paragem na origem entra uma vez.</p>{waiting.length?<ForecastTable rows={waiting}/>:<p>Sem previsão recente admissível nesta estação.</p>}{Number.isFinite(ownDays)&&ownDays<30&&<p className="notice">Previsão própria baseada em poucos dias.</p>}
 <>{independentOfficial.length>0&&<section><h3>Previsões oficiais sem associação</h3><p className="subtle">Estes valores publicados não estão associados à viagem selecionada e não formam uma comparação pareada.</p><ForecastTable rows={independentOfficial}/></section>}</>
 <h3>Chegadas seguintes da mesma associação</h3>{journeys.length>1&&<select aria-label="Viagem inferida" value={selectedJourney} onChange={e=>setJourney(e.target.value)}>{journeys.map(id=><option key={id} value={id}>ID publicado {waiting.find(f=>f.episode===id)?.train}</option>)}</select>}{following.data?<ForecastTable rows={following.data.forecasts.filter(f=>f.function==='onward'&&f.episode===selectedJourney)}/>:<p>A aguardar uma associação sustentada entre estações.</p>}{following.error&&<p className="error">{errorText(following.error)}</p>}
 <p className="subtle">Faixas com alvo nominal de 80%, calibradas contra janelas inferidas da mesma fonte. Podem estar indisponíveis no início; não são uma garantia de chegada física.</p>{data&&<p className="subtle">Avaliações amostradas nesta estação: {data.pending} pendentes · {data.evaluated} avaliados contra proxy · {data.lost_reference} sem referência após perda de suporte. Podem repetir a mesma associação; não representam cancelamentos.</p>}
 </aside><p className="subtle">Expansão por etapas configuráveis: Metro, Carris Metropolitana e um operador adicional de cada vez. As previsões próprias dependem de dados compatíveis; cada etapa requer verificar a fonte.</p></main>
}

function ForecastTable({rows}:{rows:api.MetroPatternForecast[]}){
 if(!rows.length)return <p>Sem previsão admissível para este percurso.</p>;
 return <div className="table-wrap"><table><thead><tr><th>Alvo / ID publicado</th><th>Oficial</th><th>Nossa · experimental</th></tr></thead><tbody>{rows.map(f=>{
 const age=f.source_at?(Date.now()-Date.parse(f.source_at))/1000:undefined;
 return <tr key={f.id}><td>{f.stop_name||f.stop}<small>{f.destination_name} · {f.train} · {f.platform.startsWith('visit:')?'visita':'cais'} {f.platform||'desconhecido'}</small></td><td>{time(f.official_at)}<small>{age==null?'Idade da fonte desconhecida':age>180?'Último valor conhecido':age>90?'Fonte antiga':'Fonte recente'}</small></td><td>{f.own_at?time(f.own_at):'Indisponível'}<small>{f.own_at?`${f.components.length} componentes · mínimo ${Math.min(...f.components.map(c=>c.samples))} amostras`:unavailable[f.unavailable]??f.unavailable}</small>{f.components.some(c=>c.historical_fallback)&&<small>Base histórica antiga: {f.components.filter(c=>c.historical_fallback).map(c=>`${c.oldest_date}–${c.newest_date}`).join(', ')}</small>}{f.components.some(c=>c.general_context)&&<small>Base geral: contexto de serviço menos específico</small>}<small>{f.lower_at&&f.upper_at?`Faixa 80% nominal: ${time(f.lower_at)}–${time(f.upper_at)} (${f.calibration_samples} scores)`:`Faixa indisponível (${f.calibration_samples} scores)`}</small></td></tr>
 })}</tbody></table></div>
}

function bounded(low:number|null,high:number|null){return low==null||high==null?'Indisponível':`${number(low,0)}–${number(high,0)} s`}
function EvaluationTable({rows}:{rows:api.MetroEvaluationReport[]}){
 return <section className="card"><h3>Avaliação das duas previsões</h3><p className="subtle">Concordância com janelas inferidas da mesma fonte, sem validação física. Comparação pareada usa os mesmos casos; os restantes aparecem separados. MAE e P90 são limites de erro em segundos. As viagens são associações inferidas distintas, não veículos físicos.</p>{rows.length?<div className="table-wrap"><table><thead><tr><th>Função / grupo</th><th>Amostra</th><th>Disponibilidade</th><th>Oficial · MAE / P90</th><th>Nossa · MAE / P90</th><th>Cobertura da faixa 80%</th></tr></thead><tbody>{rows.map((r,i)=><tr key={i}><td>{r.function==='waiting'?'Espera':'Chegadas seguintes'}<small>{r.cohort==='paired'?'Pareada':r.cohort==='official_only'?'Só oficial':r.cohort==='own_only'?'Só nossa':'Sem ponto'} · horizonte oficial {r.horizon<0?'desconhecido':r.horizon===0?'<5 min':r.horizon===1?'5–15 min':r.horizon===2?'15–30 min':'≥30 min'} · {r.condition}<br/>{r.mode} · {r.profile}</small></td><td>{r.evaluated} avaliadas / {r.cases} emitidas<small>{r.support==='proxy_same_source_partial_summary'?'Resumo limitado · pelo menos ':''}{r.days} dias · {r.journeys} associações {r.journey_count_complete?'':'(suporte incompleto)'}</small></td><td>{r.official_available} oficial · {r.own_available} nossa</td><td>{bounded(r.mae_official_lower,r.mae_official_upper)}<small>P90: {bounded(r.p90_official_lower,r.p90_official_upper)}</small></td><td>{bounded(r.mae_own_lower,r.mae_own_upper)}<small>P90: {bounded(r.p90_own_lower,r.p90_own_upper)}</small></td><td>{r.band_cases?`${number(100*r.band_certain/r.band_cases,0)}–${number(100*r.band_possible/r.band_cases,0)}% (${r.band_cases} casos)`:'Indisponível'}</td></tr>)}</tbody></table></div>:<p>A aguardar avaliações com referência sustentada.</p>}</section>
}
