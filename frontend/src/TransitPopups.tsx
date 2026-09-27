import {useEffect,useRef,useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import * as api from './api';
import {errorText,passengerName} from './data';
import './TransitPopups.css';

let retryAfter=0;
const popupFetch:typeof fetch=async(input,init)=>{
 if(Date.now()<retryAfter)throw new Error('Fonte temporariamente indisponível; a aguardar nova tentativa.');
 const response=await fetch(input,init);
 if(response.status===429||response.status===503){const raw=response.headers.get('Retry-After');const seconds=raw&&/^\d+$/.test(raw)?Number(raw):null;retryAfter=seconds!=null?Date.now()+seconds*1000:raw?Math.max(Date.now()+5000,Date.parse(raw)):Date.now()+5000}
 return response;
};
const options=(signal:AbortSignal)=>({signal,fetch:popupFetch});
function selection(d:api.BoardDirection){return `${d.line_key}|${d.direction_key??'unknown'}`}
function Coverage({coverage}:{coverage:api.PopupCoverage}){
 return <><p className="notice" role="status">{coverage.message}</p>{coverage.history_collection_status==='paused'&&<p className="notice" role="status">Recolha de tempos reais em pausa</p>}</>;
}
// Re-evaluate expiry locally as well as when a response arrives. Cached evidence
// never becomes actual, nor newer, merely because a popup is rendered again.
function TimeValue({value,now}:{value:api.CallTime,now:number}){
 const actual=value.actual;
 const prediction=value.prediction;
 const forecast=prediction&&Date.parse(prediction.at)>=now&&prediction.valid_until&&Date.parse(prediction.valid_until)>now?prediction:null;
 const schedule=value.schedule&&Date.parse(value.schedule.at)>=now?value.schedule:null;
 const chosen=value.kind==='actual'?actual:value.kind==='unavailable'?null:forecast??schedule;
 if(!chosen)return <span className="call-missing">{value.reason||'Sem registo real'}</span>;
 const label=chosen===actual?'Real':chosen===forecast?'Previsão':'Horário';
 const at=new Date(chosen.at),day=(d:Date)=>new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Lisbon',year:'numeric',month:'2-digit',day:'2-digit'}).format(d);
 const clock=at.toLocaleTimeString('pt-PT',{timeZone:'Europe/Lisbon',hour:'2-digit',minute:'2-digit'});
 return <span className="call-time"><time dateTime={chosen.at}>{clock}{day(at)!==day(new Date(now))&&<small>{at.toLocaleDateString('pt-PT',{timeZone:'Europe/Lisbon',day:'2-digit',month:'2-digit'})}</small>}</time><small>{label}</small>{chosen===forecast&&chosen.delay_seconds!=null&&<small>Desvio previsto: {chosen.delay_seconds>0?'+':''}{(chosen.delay_seconds/60).toLocaleString('pt-PT',{maximumFractionDigits:1})} min</small>}<small><a href={chosen.source_url} target="_blank" rel="noreferrer" aria-label={`Fonte do tempo: ${label}`} title={chosen.source_updated_at?`Atualização da fonte: ${new Date(chosen.source_updated_at).toLocaleString('pt-PT',{timeZone:'Europe/Lisbon'})}`:undefined}>Fonte</a>{chosen.source_updated_at&&` · há ${Math.max(0,Math.floor((now-Date.parse(chosen.source_updated_at))/1000))} s`}</small></span>;
}
function useClock(){const[now,setNow]=useState(Date.now());useEffect(()=>{const id=setInterval(()=>setNow(Date.now()),1000);return()=>clearInterval(id)},[]);return now}
function CallRow({call,operator,label,directionLabel,now,onStop,onVehicle}:{call:api.StopCall,operator:string,label?:string,directionLabel?:string,now:number,onVehicle?:(ref:api.VehicleReference)=>void,onStop?:(call:api.VehicleCall)=>void}){
 return <div className="transit-call" data-testid={operator==='cp'&&call.arrival.kind==='prediction'?'cp-prediction':undefined}><div>{!label&&call.stop&&call.stop_static_updated_at&&onStop?<button className="stop-link" onClick={()=>onStop({id:call.id,stop_id:call.stop_id,stop_name:call.stop_name,stop_sequence:call.stop_sequence,stop:call.stop!,stop_static_updated_at:call.stop_static_updated_at!,kind:'published_route',source_url:call.arrival.actual?.source_url??call.arrival.prediction?.source_url??call.arrival.schedule?.source_url??''})}>{passengerName(operator,call.stop_name)}</button>:<strong>{label??passengerName(operator,call.stop_name,'Paragem sem nome publicado')}</strong>}{label&&call.vehicle_ref&&onVehicle&&<button className="vehicle-link" onClick={()=>onVehicle(call.vehicle_ref!)}>Abrir veículo</button>}{call.service_label&&<small>{operator==='cp'?'Comboio':'Serviço'} {call.service_label}</small>}{label&&call.destination&&normalize(call.destination)!==normalize(directionLabel??label)&&<small>Destino: {passengerName(operator,call.destination)}</small>}</div><TimeValue value={call.arrival} now={now}/><TimeValue value={call.departure} now={now}/></div>;
}
function normalize(value:string){return value.trim().normalize('NFD').replace(/[\u0300-\u036f]/g,'').toLowerCase()}
export function StationPopup({stop,onVehicle}:{stop:api.Stop,onVehicle?:(ref:api.VehicleReference)=>void}){
 const now=useClock();const [chosen,setChosen]=useState(''),[offset,setOffset]=useState(0);
 const board=useQuery({queryKey:['stop-board',stop.id],queryFn:async({signal})=>{const data=await api.getStopBoard(stop.id,{},options(signal));if(!Array.isArray(data.directions)||!data.coverage||!data.revision)throw new Error('Resposta de sentidos indisponível.');return data},refetchInterval:5000,retry:false});
 const dirs=board.data?.directions??[];
 const active=dirs.find(d=>selection(d)===chosen)??dirs.find(d=>d.direction_key!=null)??dirs[0];
 const calls=useQuery({queryKey:['stop-calls',stop.id,active&&selection(active),offset,board.data?.revision],queryFn:async({signal})=>{
  try{const data=await api.listStopCalls(stop.id,{lineKey:active!.line_key,directionKey:active!.direction_key??'unknown',limit:25,offset,revision:board.data!.revision},options(signal));if(!data.coverage||!data.page||!Array.isArray(data.data))throw new Error('Resposta de horários indisponível.');return data}
  catch(error){if((error as {status?:number}).status===410){setOffset(0);void board.refetch()}throw error}
 },enabled:!!active&&!!board.data?.revision,retry:false});
 const lines=[...new Set(dirs.map(d=>d.line_key))];
 return <div className="transit-popup" aria-busy={board.isFetching||calls.isFetching}>
  {board.error&&<p className="error" role="alert">{errorText(board.error)}</p>}
  {board.data&&<Coverage coverage={board.data.coverage}/>}
  {!board.data&&board.isPending&&<p role="status">A carregar sentidos…</p>}
  <div className="direction-matrix" aria-label="Linhas e sentidos">{lines.map(line=>{const row=dirs.filter(d=>d.line_key===line);return <div className="direction-line" key={line}><h4><i style={{background:row[0].color}}/>{passengerName(stop.operator_id,row[0].line_name)}</h4><div>{row.map(d=><button key={selection(d)} aria-pressed={active&&selection(active)===selection(d)} onClick={()=>{setChosen(selection(d));setOffset(0)}}><strong>{passengerName(stop.operator_id,d.label,'Sentido não identificado')}</strong><small>{d.count==null?'Cobertura por confirmar':`${d.count} ${d.count===1?'viagem':'viagens'}`}</small></button>)}</div></div>})}</div>
  {active&&<section className="direction-detail"><h4 aria-live="polite">Próximas chegadas e partidas <span>→ {passengerName(stop.operator_id,active.label)}</span></h4><div className="transit-call call-head"><span>Viagem</span><span>Chegada</span><span>Partida</span></div>
   {calls.data?.data.map((c,index)=><CallRow key={c.id} call={c} operator={stop.operator_id} label={`Viagem ${offset+index+1}`} directionLabel={active.label} now={now} onVehicle={onVehicle}/>)}
   {calls.isPending&&<p role="status">A carregar horários…</p>}
   {calls.error&&<p className="error" role="alert">{errorText(calls.error)}</p>}
   {calls.isSuccess&&!calls.data.data.length&&<p className="empty">{calls.data.coverage.status==='unavailable'?'Horários indisponíveis para este sentido.':calls.data.coverage.status==='stale'?'Dados desatualizados; próximas viagens por confirmar.':'Sem chegadas ou partidas nas próximas duas horas.'}</p>}
   {calls.data&&<div className="pagination"><button disabled={!offset} onClick={()=>setOffset(Math.max(0,offset-25))}>Anterior</button><span>{calls.data.page.total} viagens</span><button disabled={!calls.data.page.has_more} onClick={()=>setOffset(offset+25)}>Seguinte</button></div>}
  </section>}
  {board.isSuccess&&!dirs.length&&<p className="empty">Sentidos e horários indisponíveis nesta fonte.</p>}
 </div>;
}
export function VehiclePopup({vehicle,onResolved,onStop}:{vehicle:api.Vehicle,onResolved?:(resolved:boolean)=>void,onStop?:(call:api.VehicleCall)=>void}){
 const now=useClock();const [offset,setOffset]=useState<number>(),navigationRevision=useRef<string|undefined>(undefined),focus=useRef<HTMLElement>(null),lastJourney=useRef<string|null>(null);
 const journey=useQuery({queryKey:['vehicle-journey',vehicle.id,offset],queryFn:async({signal})=>{
  const revision=navigationRevision.current;navigationRevision.current=undefined;
  try{const data=await api.getVehicleJourney(vehicle.id,{limit:100,offset,revision},options(signal));if(!data.association||!data.coverage||!data.page||!Array.isArray(data.data))throw new Error('Resposta de percurso indisponível.');return data}
  catch(error){if((error as {status?:number}).status===410){setOffset(undefined);return api.getVehicleJourney(vehicle.id,{limit:100},options(signal))}throw error}
 },refetchInterval:5000,retry:false});
 const data=journey.data;
 useEffect(()=>{onResolved?.(data?.association==='resolved')},[data?.association,onResolved]);
 const progressFresh=!vehicle.last_known&&!vehicle.stale&&now-Date.parse(vehicle.observed_at)<90000;
 useEffect(()=>{if(!data)return;if(lastJourney.current!==data.journey_id){lastJourney.current=data.journey_id;setOffset(undefined)}},[data?.journey_id]);
 useEffect(()=>{if(data?.next_index!=null&&offset===undefined)focus.current?.scrollIntoView({block:'nearest'})},[data?.journey_id,data?.next_index,offset]);
 function go(to:number){navigationRevision.current=data?.page.revision??undefined;setOffset(to)}
 return <section className={`transit-popup vehicle-journey${vehicle.operator_id==='cp'?' cp-calls':''}`} aria-busy={journey.isFetching}>
  <h4>Percurso do veículo {data?.direction&&<span>→ {passengerName(vehicle.operator_id,data.direction)}</span>}</h4>
  {journey.error&&<p className="error" role="alert">{errorText(journey.error)}</p>}
  {!data&&journey.isPending&&<p role="status">A carregar percurso…</p>}
  {data&&<><Coverage coverage={data.coverage}/>{data.message&&<p className="notice">{data.message}</p>}
   {data.association==='resolved'&&<>
    {data.destination&&normalize(data.destination)!==normalize(data.direction)&&<p>Destino desta viagem: <strong>{passengerName(vehicle.operator_id,data.destination)}</strong></p>}
    <p className="subtle">{data.complete?'Percurso completo':'Percurso parcial'} · {data.page.total} visitas · {!progressFresh||data.progress==='unknown'?'Progresso não confirmado':data.progress==='estimated'?'Progresso estimado':'Progresso publicado pelo operador'}</p>
    <div className="journey-actions"><button disabled={!data.page.offset} onClick={()=>go(0)}>Ver início</button><button disabled={data.next_index==null||!progressFresh} onClick={()=>{if(data.next_index!=null){const target=Math.floor(data.next_index/100)*100;if(target!==data.page.offset)go(target);else focus.current?.scrollIntoView({block:'nearest'})}}}>Ver próxima paragem</button></div>
    <div className="transit-call call-head"><span>Paragem</span><span>Chegada</span><span>Partida</span></div>
    <ol className="journey-timeline" start={data.page.offset+1}>{data.data.map((call,index)=>{const next=progressFresh&&data.next_index===data.page.offset+index;return <li key={call.id} className={`visit-${call.phase}${next?' visit-next':''}`}><article ref={next?focus:undefined}><div className="visit-label">{next?data.progress==='estimated'?'Próxima paragem estimada':'Próxima paragem':call.phase==='previous'?'Paragem anterior':call.phase==='future'?'Por percorrer':call.phase==='current'?progressFresh?'Paragem atual':'Última paragem indicada':'Progresso por confirmar'}</div><CallRow call={call} operator={vehicle.operator_id} now={now} onStop={onStop}/></article></li>})}</ol>
    <div className="pagination"><button disabled={!data.page.offset} onClick={()=>go(Math.max(0,data.page.offset-100))}>Anterior</button><span>{data.page.offset+1}–{data.page.offset+data.data.length} de {data.page.total}</span><button disabled={!data.page.has_more} onClick={()=>go(data.page.offset+data.page.limit)}>Seguinte</button></div>
   </>}
  </>}
 </section>;
}
