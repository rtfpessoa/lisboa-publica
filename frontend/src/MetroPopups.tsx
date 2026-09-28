import {useContext,useEffect,useLayoutEffect,useRef,useState} from 'react';
import {MetroLiveContext} from './useMetroLive';
import {useStationReading} from './useStationReading';
import type {CallTime,MetroTrain,Stop,StopCall,Vehicle} from './api';
import './TransitPopups.css';

function useClock(before?:()=>void){const callback=useRef(before);callback.current=before;const [now,setNow]=useState(Date.now());useEffect(()=>{const tick=()=>{callback.current?.();setNow(Date.now())};const timer=setInterval(tick,1000);document.addEventListener('visibilitychange',tick);return()=>{clearInterval(timer);document.removeEventListener('visibilitychange',tick)}},[]);return now}
function usable(value:CallTime|undefined,now:number){const p=value?.prediction;return p&&Date.parse(p.at)>=now&&!!p.valid_until&&Date.parse(p.valid_until)>now?p:null}
export function MetroTime({value,now,departure=false}:{value:CallTime,now:number,departure?:boolean}){
 const inferred=value.inferred;
 if(inferred){const label=inferred.mode==='model_departure'?'Partida estimada (modelo)':'Chegada inferida';return <span className="call-time"><time dateTime={inferred.at}>≈ {new Date(inferred.at).toLocaleTimeString('pt-PT',{hour:'2-digit',minute:'2-digit',timeZone:'Europe/Lisbon'})}</time><small>{label}</small><details><summary>Ver evidência</summary><small>{inferred.reason} · {inferred.window_start} – {inferred.window_end} · {inferred.model_version} · {inferred.persistence==='committed'?'Guardado (retenção limitada)':inferred.persistence==='pending'?'A guardar':'Gravação indisponível'}</small></details></span>}
 const prediction=usable(value,now);
 if(!prediction)return <span className="call-missing">{departure?'Sem dados de partida':'Sem previsão atual'}</span>;
 const seconds=Math.max(0,Math.ceil((Date.parse(prediction.at)-now)/1000));
 return <span className="call-time"><time dateTime={prediction.at}>{Math.floor(seconds/60)} min {String(seconds%60).padStart(2,'0')} s</time><small>Previsão oficial</small><small title={`Fonte: ${prediction.source_updated_at}`}>há {Math.max(0,Math.floor((now-Date.parse(prediction.source_updated_at??''))/1000))} s</small></span>;
}
function OwnTime({call,now}:{call:StopCall|undefined,now:number}){const own=call?.own_prediction;if(!own||!own.valid_until||Date.parse(own.valid_until)<=now||Date.parse(own.at)<now)return <small className="call-missing">Própria: sem previsão atual</small>;const seconds=Math.ceil((Date.parse(own.at)-now)/1000);return <small title={`Fonte: ${own.source_updated_at??'não indicada'}`}>Própria (experimental): {Math.floor(seconds/60)} min {String(seconds%60).padStart(2,'0')} s</small>}
function Status(){const live=useContext(MetroLiveContext);return <><p className="notice">Referências e progresso inferidos; as posições são estimadas. As horas inferidas não são tempos reais medidos.</p>{live.error&&<p role="status" className="notice">{live.error} A mostrar o último quadro recebido.</p>}{!live.frame&&<p role="status">A aguardar dados Metro…</p>}{live.frame?.history_status==='paused'&&<p role="status">Gravação em pausa; podem existir lacunas no histórico.</p>}</>}
export function MetroJourneyPopup({vehicle}:{vehicle:Vehicle}){
 const live=useContext(MetroLiveContext),root=useRef<HTMLDivElement>(null),reading=useStationReading(root),now=useClock(()=>reading.prepare(true)),next=useRef<HTMLElement>(null);
 useEffect(()=>live.beforeFrame?.(()=>reading.prepare(true)),[live.beforeFrame,reading.prepare]);
 useLayoutEffect(reading.restore,[live.frame,now]);
 const train=live.frame?.trains.find(t=>t.journey_id===live.frame?.selected_journey_id);
 const supported=!!train&&train.association==='supported'&&now<Date.parse(train.valid_until);
 const visitStop=(call:StopCall)=>{const stop=live.stops.find(s=>s.id===call.stop_id);if(stop&&call.stop_plan_id===live.frame?.plan_id&&live.stopsPlanId===live.frame?.plan_id)live.onStop?.(stop)};
 return <div ref={root} data-metro-revision={live.frame?.revision} data-metro-published-at={live.frame?.published_at} className="transit-popup vehicle-journey"><h4>Comboio {train?.reference??vehicle.source_id} {train?.destination&&<span>→ {train.destination}</span>}</h4><Status/>
  {train?<><p className="subtle">{supported?'Viagem inferida com suporte atual':train.reason||'Continuidade por confirmar'}</p><div className="journey-actions"><button disabled={!supported||train.next_index==null} onClick={()=>next.current?.scrollIntoView({block:'nearest'})}>Ver próxima estação</button><button disabled={!supported} onClick={live.resumeFollow}>{live.followPaused?'Retomar seguimento':'A seguir comboio'}</button></div>
   <div className="transit-call call-head"><span>Estação</span><span>Chegada</span><span>Partida</span></div><ol className="journey-timeline">{train.calls.map((call,index)=><li key={call.id} className={`visit-${supported?call.phase:'unknown'}${supported&&train.next_index===index?' visit-next':''}`}><article data-call-id={call.id} ref={supported&&train.next_index===index?next:undefined}><div className="visit-label">{supported&&train.current_index===index?'Estação atual inferida':supported&&train.next_index===index?'Próxima estação estimada':call.phase==='previous'?'Estação anterior':call.phase==='current'?'Estação atual estimada':'Progresso por confirmar'}</div><div className="transit-call"><div><button className="stop-link" onClick={()=>visitStop(call)} disabled={!live.stops.some(s=>s.id===call.stop_id)||call.stop_plan_id!==live.frame?.plan_id||live.stopsPlanId!==live.frame?.plan_id}>{call.stop_name}</button></div><div><MetroTime value={call.arrival} now={now}/><OwnTime call={call} now={now}/></div><MetroTime value={call.departure} now={now} departure/></div></article></li>)}</ol>
  </>:live.frame&&<p>Sem associação segura à viagem deste comboio.</p>}
 </div>;
}
export function MetroStationPopup({stop}:{stop:Stop}){
 const live=useContext(MetroLiveContext),root=useRef<HTMLDivElement>(null),reading=useStationReading(root),now=useClock(()=>reading.prepare(true));
 useEffect(()=>live.beforeFrame?.(()=>reading.prepare(true)),[live.beforeFrame,reading.prepare]);
 const dirs=live.frame?.directions??[],[selection,setSelection]=useState('');
 const key=(line:string,direction:string|null)=>`${line}|${direction??'unknown'}`;
 const active=dirs.find(d=>key(d.line_key,d.direction_key)===selection)??(!selection?dirs[0]:undefined);
 useLayoutEffect(reading.restore,[live.frame,now]);
 const trains=live.frame?.trains.filter(t=>active&&t.route_id===active.line_key&&t.direction_code===active.direction_key)??[];
 const current=(t:MetroTrain)=>t.association==='supported'&&Date.parse(t.valid_until)>now;
 const call=(t:MetroTrain)=>t.calls.find(c=>c.stop_id===stop.id);
 const own=(t:MetroTrain)=>{const p=call(t)?.own_prediction;return p&&p.valid_until&&Date.parse(p.at)>=now&&Date.parse(p.valid_until)>now?p:null};
 const upcoming=(t:MetroTrain)=>current(t)&&!!call(t)&&(['future','current'].includes(call(t)!.phase)||!!usable(call(t)?.arrival,now)||!!own(t));
 const sort=(rows:MetroTrain[])=>rows.sort((a,b)=>{const ap=usable(call(a)?.arrival,now)??own(a),bp=usable(call(b)?.arrival,now)??own(b);return (ap?Date.parse(ap.at):Infinity)-(bp?Date.parse(bp.at):Infinity)||a.journey_id.localeCompare(b.journey_id)});
 const open=(t:MetroTrain)=>{const v=live.frame?.vehicles.find(v=>v.id===t.vehicle_id);if(v&&current(t))live.onVehicle?.(v)};
 const rows=(items:MetroTrain[])=>sort(items).map(t=><div key={t.journey_id} data-call-id={t.journey_id} className="transit-call"><div><strong>Comboio {t.reference}</strong><small>→ {t.destination}</small>{t.vehicle_id&&current(t)&&<button className="vehicle-link" onClick={()=>open(t)}>Abrir comboio</button>}{!upcoming(t)&&<small>{!current(t)?'Dados antigos ou associação suspensa':call(t)?.phase==='previous'?'Já passou nesta estação':!call(t)?'Termina antes desta estação':'Passagem por confirmar'}</small>}</div><div><MetroTime value={call(t)?.arrival??{kind:'unavailable',at:null,reason:'',actual:null,prediction:null,schedule:null}} now={now}/><OwnTime call={call(t)} now={now}/></div></div>);
 return <div ref={root} data-metro-revision={live.frame?.revision} data-metro-published-at={live.frame?.published_at} className="transit-popup station-popup"><Status/><div className="direction-matrix" aria-label="Linhas e sentidos">{[...new Set(dirs.map(d=>d.line_key))].map(line=><div className="direction-line" key={line}><h4>{dirs.find(d=>d.line_key===line)?.line_name}</h4><div>{dirs.filter(d=>d.line_key===line).map(d=><button key={key(d.line_key,d.direction_key)} aria-pressed={d===active} onClick={()=>{reading.prepare(false);setSelection(key(d.line_key,d.direction_key))}}>{d.label}</button>)}</div></div>)}</div>
 {active?<><h4>A chegar a esta estação</h4>{rows(trains.filter(upcoming))}{!trains.some(upcoming)&&<p>Sem comboios identificados com passagem seguinte confirmada.</p>}<h4>Outros neste sentido</h4>{rows(trains.filter(t=>current(t)&&!upcoming(t)))}{trains.some(t=>!current(t))&&<><h4>Dados antigos ou ambíguos</h4>{rows(trains.filter(t=>!current(t)))}</>}<p className="subtle">{trains.filter(current).length} comboios identificados neste sentido. A fonte pode não publicar todos os comboios.</p></>:live.frame&&<p>Sentido indisponível ou alterado; selecione um sentido atual.</p>}
 {!!live.frame?.unassociated_forecasts.length&&<section><h4>Previsões oficiais sem associação à viagem</h4>{live.frame.unassociated_forecasts.filter(c=>active&&c.line_key===active.line_key&&(c.direction_key===active.direction_key||c.direction_key==null)).map(c=><div className="transit-call" key={c.id}><div>Comboio {c.service_label}<small>→ {c.destination}</small>{c.direction_key==null&&<small>Sentido não confirmado</small>}</div><MetroTime value={c.arrival} now={now}/></div>)}</section>}
 </div>;
}
