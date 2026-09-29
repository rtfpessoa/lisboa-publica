import {useContext,useEffect,useLayoutEffect,useRef,useState} from 'react';
import {MetroLiveContext} from './useMetroLive';
import {useStationReading} from './useStationReading';
import type {CallTime,MetroTrain,Stop,StopCall,Vehicle} from './api';
import './TransitPopups.css';
import {metroCurrentDirection} from './metroEvidence';

function useClock(before?:()=>void){const callback=useRef(before);callback.current=before;const [now,setNow]=useState(Date.now());useEffect(()=>{const tick=()=>{callback.current?.();setNow(Date.now())};const timer=setInterval(tick,500);document.addEventListener('visibilitychange',tick);return()=>{clearInterval(timer);document.removeEventListener('visibilitychange',tick)}},[]);return now}
function usable(value:CallTime|undefined,now:number){const p=value?.prediction;return p&&Date.parse(p.at)>=now&&!!p.valid_until&&Date.parse(p.valid_until)>now?p:null}
export function MetroTime({value,now,departure=false}:{value:CallTime,now:number,departure?:boolean}){
 const inferred=value.inferred;
 if(inferred){const label=inferred.mode==='model_departure'?'Partida estimada (modelo)':'Chegada inferida';return <span className="call-time"><time dateTime={inferred.at}>≈ {new Date(inferred.at).toLocaleTimeString('pt-PT',{hour:'2-digit',minute:'2-digit',timeZone:'Europe/Lisbon'})}</time><small>{label}</small><details><summary>Ver evidência</summary><small>{inferred.reason} · {inferred.window_start} – {inferred.window_end} · {inferred.model_version} · {inferred.persistence==='committed'?'Guardado (retenção limitada)':inferred.persistence==='pending'?'A guardar':'Gravação indisponível'}</small></details></span>}
 const prediction=usable(value,now);
 if(!prediction)return <span className="call-missing">{value.reason==='Estimativa retirada'?'Estimativa retirada':departure?'Sem dados de partida':'Sem previsão atual'}</span>;
 const seconds=Math.max(0,Math.ceil((Date.parse(prediction.at)-now)/1000));
 return <span className="call-time"><time dateTime={prediction.at}>{Math.floor(seconds/60)} min {String(seconds%60).padStart(2,'0')} s</time><small>Previsão oficial</small><small title={`Fonte: ${prediction.source_updated_at}`}>há {Math.max(0,Math.floor((now-Date.parse(prediction.source_updated_at??''))/1000))} s</small></span>;
}
function ownUsable(call:StopCall|undefined,now:number){const p=call?.own_prediction;return p&&p.valid_until&&Date.parse(p.valid_until)>now&&Date.parse(p.at)>=now?p:null}
function OwnTime({call,now}:{call:StopCall|undefined,now:number}){const own=ownUsable(call,now);if(!own)return <small className="call-missing">Nossa: sem previsão atual</small>;const seconds=Math.ceil((Date.parse(own.at)-now)/1000);return <small title={`Fonte: ${own.source_updated_at??'não indicada'}`}>Nossa previsão (experimental): <time dateTime={own.at}>{Math.floor(seconds/60)} min {String(seconds%60).padStart(2,'0')} s</time></small>}
function Status(){const live=useContext(MetroLiveContext);return <><p className="notice">Referências e progresso inferidos; as posições são estimadas. As horas inferidas não são tempos reais medidos.</p>{live.error&&<p role="status" className="notice">{live.error} A mostrar o último quadro recebido.</p>}{!live.frame&&<p role="status">A aguardar dados Metro…</p>}{live.frame?.history_status==='paused'&&<p role="status">Gravação em pausa; podem existir lacunas no histórico.</p>}</>}
export function MetroJourneyPopup({vehicle}:{vehicle:Vehicle}){
 const live=useContext(MetroLiveContext),root=useRef<HTMLDivElement>(null),reading=useStationReading(root),now=useClock(()=>reading.prepare(true)),next=useRef<HTMLElement>(null);
 useEffect(()=>live.beforeFrame?.(()=>reading.prepare(true)),[live.beforeFrame,reading.prepare]);
 useLayoutEffect(reading.restore,[live.frame,now]);
 const train=live.frame?.trains.find(t=>t.journey_id===live.frame?.selected_journey_id)??live.retainedJourney;
 const supported=metroCurrentDirection(train,now);
 const currentJourney=live.frame?.trains.find(t=>t.vehicle_id===vehicle.id&&t.journey_id!==train?.journey_id&&metroCurrentDirection(t,now));
 const visitStop=(call:StopCall)=>{const stop=live.stops.find(s=>s.id===call.stop_id);if(stop&&call.stop_plan_id===live.frame?.plan_id&&live.stopsPlanId===live.frame?.plan_id)live.onStop?.(stop)};
 return <div ref={root} data-metro-revision={live.frame?.revision} data-metro-published-at={live.frame?.published_at} className="transit-popup vehicle-journey"><h4>Comboio {train?.reference??vehicle.source_id} {supported&&train?.destination&&<span>→ {train.destination} (sentido inferido)</span>}</h4><Status/>{train?.lifecycle&&train.lifecycle.state!=='active'&&<p role="status">{train.lifecycle.reason}</p>}{train?.model_projection&&supported&&<p className="notice">Posição local estimada (modelo); avaliação a cada 500 ms.</p>}
  {live.frame?.recovery&&['recovering','partial','expired','unavailable','corrupt'].includes(live.frame.recovery.status)&&<p role="status">{live.frame.recovery.reason}</p>}
  {train?<><p className="subtle">{supported?'Viagem inferida com suporte atual':train.reason||'Continuidade por confirmar'}</p><div className="journey-actions"><button disabled={!supported||train.next_index==null} onClick={()=>next.current?.scrollIntoView({block:'nearest'})}>Ver próxima estação</button><button disabled={!supported} onClick={live.resumeFollow}>{live.followPaused?'Retomar seguimento':'A seguir comboio'}</button>{currentJourney&&<button onClick={()=>live.selectJourney?.(currentJourney.journey_id)}>Abrir viagem atual</button>}</div>
   <div className="transit-call call-head"><span>Estação</span><span>Chegada</span><span>Partida</span></div><ol className="journey-timeline">{train.calls.map((call,index)=><li key={call.id} className={`visit-${supported?call.phase:'unknown'}${supported&&train.next_index===index?' visit-next':''}`}><article data-call-id={call.id} ref={supported&&train.next_index===index?next:undefined}><div className="visit-label">{supported&&train.current_index===index?'Estação atual inferida':supported&&train.next_index===index?'Próxima estação estimada':call.phase==='previous'?'Estação anterior':call.phase==='current'?'Estação atual estimada':'Progresso por confirmar'}</div><div className="transit-call"><div><button className="stop-link" onClick={()=>visitStop(call)} disabled={!live.stops.some(s=>s.id===call.stop_id)||call.stop_plan_id!==live.frame?.plan_id||live.stopsPlanId!==live.frame?.plan_id}>{call.stop_name}</button></div><div><MetroTime value={call.arrival} now={now}/><OwnTime call={call} now={now}/></div><MetroTime value={call.departure} now={now} departure/>{call.departure_revisions&&call.departure_revisions.length>0&&<details><summary>Revisões da partida</summary>{call.departure_revisions.map(r=><small key={r.revision}>{r.status==='withdrawn'?'Estimativa retirada':'Partida estimada (modelo)'} · {r.source_at} · {r.reason}</small>)}</details>}</div></article></li>)}</ol>
  </>:live.frame&&<p>{live.frame.association_reason??'Sem dados atuais para confirmar a viagem'}</p>}
  {!supported&&<MetroForecastGroups now={now}/>}
 </div>;
}
function ForecastRow({call,now,reference}:{call:StopCall,now:number,reference?:string}){
 const association=call.metro_forecast;
 const source=association?association.source_reference:call.service_label??reference;
 const label=source?`Comboio ${source}`:association?.estimated_reference?`Comboio ${association.estimated_reference} (associação estimada)`:'Comboio por identificar';
 return <div className="transit-call" data-call-id={call.id}><div>{call.stop_name}<small>{label} → {call.destination}</small>{call.direction_key==null&&<small>Sentido não confirmado</small>}{association?.limitations.includes('station_prediction_conflict')&&<small>Previsões diferentes nesta estação; associação por confirmar</small>}</div><div><MetroTime value={call.arrival} now={now}/><OwnTime call={call} now={now}/></div></div>;
}
function MetroForecastGroups({now}:{now:number}){
 const live=useContext(MetroLiveContext),hasTime=(call:StopCall)=>!!usable(call.arrival,now)||!!ownUsable(call,now);
 const contexts=live.frame?.forecast_contexts?.filter(c=>c.status==='admissible'&&c.calls.some(hasTime))??[];
 if(!contexts.length)return <p className="subtle">Viagem por confirmar</p>;
 const keys=[...new Set(contexts.map(c=>`${c.route_id}|${c.direction_code??'unknown'}`))];
 return <section><h4>Viagem por confirmar</h4><p className="subtle">Previsões por sentido; a associação ao comboio continua por confirmar.</p>{keys.map(key=><section key={key}><h5>{contexts.filter(c=>`${c.route_id}|${c.direction_code??'unknown'}`===key).map(c=>c.destination).filter((v,i,all)=>all.indexOf(v)===i).join(' / ')}</h5>{contexts.filter(c=>`${c.route_id}|${c.direction_code??'unknown'}`===key).flatMap(c=>c.calls.filter(hasTime).map(call=><ForecastRow key={call.id} call={call} reference={c.reference} now={now}/>))}</section>)}</section>;
}
export function MetroStationPopup({stop}:{stop:Stop}){
 const live=useContext(MetroLiveContext),root=useRef<HTMLDivElement>(null),reading=useStationReading(root),now=useClock(()=>reading.prepare(true));
 useEffect(()=>live.beforeFrame?.(()=>reading.prepare(true)),[live.beforeFrame,reading.prepare]);
 const dirs=live.frame?.directions??[],[selection,setSelection]=useState('');
 const key=(line:string,direction:string|null)=>`${line}|${direction??'unknown'}`;
 const active=dirs.find(d=>key(d.line_key,d.direction_key)===selection)??(!selection?dirs[0]:undefined);
 useLayoutEffect(reading.restore,[live.frame,now]);
 const trains=live.frame?.trains.filter(t=>active&&t.route_id===active.line_key&&t.direction_code===active.direction_key)??[];
 const current=(t:MetroTrain)=>metroCurrentDirection(t,now);
 const call=(t:MetroTrain)=>t.calls.find(c=>c.stop_id===stop.id);
 const own=(t:MetroTrain)=>ownUsable(call(t),now);
 const upcoming=(t:MetroTrain)=>current(t)&&!!call(t)&&(['future','current'].includes(call(t)!.phase)||!!usable(call(t)?.arrival,now)||!!own(t));
 const sort=(rows:MetroTrain[])=>rows.sort((a,b)=>{const ap=usable(call(a)?.arrival,now)??own(a),bp=usable(call(b)?.arrival,now)??own(b);return (ap?Date.parse(ap.at):Infinity)-(bp?Date.parse(bp.at):Infinity)||a.journey_id.localeCompare(b.journey_id)});
 const open=(t:MetroTrain)=>{const v=live.frame?.vehicles.find(v=>v.id===t.vehicle_id);if(v&&current(t))live.onVehicle?.(v)};
 const rows=(items:MetroTrain[])=>sort(items).map(t=><div key={t.journey_id} data-call-id={t.journey_id} className="transit-call"><div><strong>Comboio {t.reference}</strong><small>→ {t.destination}</small>{t.vehicle_id&&current(t)&&<button className="vehicle-link" onClick={()=>open(t)}>Abrir comboio</button>}{!upcoming(t)&&<small>{!current(t)?'Dados antigos ou associação suspensa':call(t)?.phase==='previous'?'Já passou nesta estação':!call(t)?'Termina antes desta estação':'Passagem por confirmar'}</small>}</div><div><MetroTime value={call(t)?.arrival??{kind:'unavailable',at:null,reason:'',actual:null,prediction:null,schedule:null}} now={now}/><OwnTime call={call(t)} now={now}/></div></div>);
 return <div ref={root} data-metro-revision={live.frame?.revision} data-metro-published-at={live.frame?.published_at} className="transit-popup station-popup"><Status/><div className="direction-matrix" aria-label="Linhas e sentidos">{[...new Set(dirs.map(d=>d.line_key))].map(line=><div className="direction-line" key={line}><h4>{dirs.find(d=>d.line_key===line)?.line_name}</h4><div>{dirs.filter(d=>d.line_key===line).map(d=><button key={key(d.line_key,d.direction_key)} aria-pressed={d===active} onClick={()=>{reading.prepare(false);setSelection(key(d.line_key,d.direction_key))}}>{d.label}</button>)}</div></div>)}</div>
 {active?<><h4>A chegar a esta estação</h4>{rows(trains.filter(upcoming))}{!trains.some(upcoming)&&<p>Sem comboios identificados com passagem seguinte confirmada.</p>}<h4>Outros neste sentido</h4>{rows(trains.filter(t=>current(t)&&!upcoming(t)))}{trains.some(t=>!current(t))&&<><h4>Dados antigos ou ambíguos</h4>{rows(trains.filter(t=>!current(t)))}</>}<p className="subtle">{trains.filter(current).length} comboios identificados neste sentido. A fonte pode não publicar todos os comboios.</p></>:live.frame&&<p>Sentido indisponível ou alterado; selecione um sentido atual.</p>}
 {active&&<><section><h4>Previsões sem viagem atual confirmada</h4>{live.frame?.unassociated_forecasts.filter(c=>c.line_key===active.line_key&&c.direction_key===active.direction_key&&(usable(c.arrival,now)||ownUsable(c,now))).map(c=><ForecastRow key={c.id} call={c} now={now}/>)}</section>{live.frame?.unassociated_forecasts.some(c=>c.line_key===active.line_key&&c.direction_key==null&&(usable(c.arrival,now)||ownUsable(c,now)))&&<section><h4>Previsões com sentido por confirmar</h4>{live.frame.unassociated_forecasts.filter(c=>c.line_key===active.line_key&&c.direction_key==null&&(usable(c.arrival,now)||ownUsable(c,now))).map(c=><ForecastRow key={c.id} call={c} now={now}/>)}</section>}</>}

 </div>;
}
