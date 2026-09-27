import {useEffect,useLayoutEffect,useRef,useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import * as api from './api';
import {errorText,passengerName} from './data';
import './TransitPopups.css';
import {directionSelection as selection,useStationBoard} from './useStationBoard';
import {useStationReading} from './useStationReading';

let retryAfter=0;
const popupFetch:typeof fetch=async(input,init)=>{
 if(Date.now()<retryAfter)throw new Error('Fonte temporariamente indisponível; a aguardar nova tentativa.');
 const response=await fetch(input,init);
 if(response.status===429||response.status===503){const raw=response.headers.get('Retry-After');const seconds=raw&&/^\d+$/.test(raw)?Number(raw):null;retryAfter=seconds!=null?Date.now()+seconds*1000:raw?Math.max(Date.now()+5000,Date.parse(raw)):Date.now()+5000}
 return response;
};
const options=(signal:AbortSignal)=>({signal,fetch:popupFetch});
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
function useClock(beforeTick?:()=>void){const[now,setNow]=useState(Date.now()),before=useRef(beforeTick);before.current=beforeTick;useEffect(()=>{const id=setInterval(()=>{before.current?.();setNow(Date.now())},1000);return()=>clearInterval(id)},[]);return now}
function CallRow({call,operator,label,directionLabel,now,onStop,onVehicle,routeOnly=false}:{call:api.StopCall,operator:string,label?:string,directionLabel?:string,now:number,onVehicle?:(ref:api.VehicleReference)=>void,onStop?:(call:api.VehicleCall)=>void,routeOnly?:boolean}){
 return <div className={`transit-call${routeOnly?' route-call':''}`} data-call-id={call.id} data-testid={operator==='cp'&&call.arrival.kind==='prediction'?'cp-prediction':undefined}><div>{!label&&call.stop&&call.stop_static_updated_at&&onStop?<button className="stop-link" onClick={()=>onStop({id:call.id,stop_id:call.stop_id,stop_name:call.stop_name,stop_sequence:call.stop_sequence,stop:call.stop!,stop_static_updated_at:call.stop_static_updated_at!,stop_plan_id:call.stop_plan_id,kind:'published_route',source_url:call.arrival.actual?.source_url??call.arrival.prediction?.source_url??call.arrival.schedule?.source_url??''})}>{passengerName(operator,call.stop_name)}</button>:<strong>{label??passengerName(operator,call.stop_name,'Paragem sem nome publicado')}</strong>}{label&&call.vehicle_ref&&onVehicle&&<button className="vehicle-link" onClick={()=>onVehicle(call.vehicle_ref!)}>Abrir veículo</button>}{call.service_label&&<small>{operator==='cp'?'Comboio':'Serviço'} {call.service_label}</small>}{label&&call.destination&&normalize(call.destination)!==normalize(directionLabel??label)&&<small>Destino: {passengerName(operator,call.destination)}</small>}</div>{!routeOnly&&<><TimeValue value={call.arrival} now={now}/><TimeValue value={call.departure} now={now}/></>}</div>;
}
function normalize(value:string){return value.trim().normalize('NFD').replace(/[\u0300-\u036f]/g,'').toLowerCase()}
export function StationPopup({stop,onVehicle}:{stop:api.Stop,onVehicle?:(ref:api.VehicleReference)=>void}){
 const root=useRef<HTMLDivElement>(null);
 const {prepare:prepareReading,restore}=useStationReading(root);
 const now=useClock(()=>prepareReading(true));
 const {frame,error,loading,choose}=useStationBoard(stop.id,options,prepareReading);
 useLayoutEffect(restore,[frame,now,restore]);
 const dirs=frame?.board.directions??[],active=frame?.active,calls=frame?.calls,offset=frame?.offset??0;
 const lines=[...new Set(dirs.map(d=>d.line_key))];
 const coverage=frame&&stationCoverage(calls?.coverage??frame.board.coverage,calls,now);
 return <div ref={root} className="transit-popup station-popup" aria-busy={loading}>
  {coverage&&<Coverage coverage={coverage}/>}
  {!frame&&loading&&<p role="status">A carregar sentidos e horários…</p>}
  <div className="direction-matrix" aria-label="Linhas e sentidos">{lines.map(line=>{const row=dirs.filter(d=>d.line_key===line);return <div className="direction-line" key={line}><h4><i style={{background:row[0].color}}/>{passengerName(stop.operator_id,row[0].line_name)}</h4><div>{row.map(d=><button key={selection(d)} aria-pressed={!!active&&selection(active)===selection(d)} onClick={()=>choose(selection(d))}><strong>{passengerName(stop.operator_id,d.label,'Sentido não identificado')}</strong></button>)}</div></div>})}</div>
  {active&&<section className="direction-detail"><h4>Próximas chegadas e partidas <span>→ {passengerName(stop.operator_id,active.label)}</span></h4><div className="transit-call call-head"><span>Resultado</span><span>Chegada</span><span>Partida</span></div>
   {frame?.missing&&<p className="empty" role="status">Este sentido deixou de estar disponível. Escolha outro sentido para consultar os horários.</p>}
   {calls?.data.map((c,index)=><CallRow key={c.id} call={c} operator={stop.operator_id} label={`Resultado ${offset+index+1}`} directionLabel={active.label} now={now} onVehicle={onVehicle}/>)}
   {calls&&!calls.data.length&&<p className="empty">{coverage?.status==='unavailable'?'Horários indisponíveis para este sentido.':coverage?.status==='stale'?'Dados desatualizados; próximos resultados por confirmar.':'Sem resultados disponíveis para este sentido nas próximas duas horas.'}</p>}
   {calls&&<div className="pagination"><button disabled={!offset} onClick={()=>choose(selection(active),Math.max(0,offset-25))}>Anterior</button><span>{calls.page.total} resultados</span><button disabled={!calls.page.has_more} onClick={()=>choose(selection(active),offset+25)}>Seguinte</button></div>}
  </section>}
  {frame&&!dirs.length&&!active&&<p className="empty">Sentidos e horários indisponíveis nesta fonte.</p>}
  {!!error&&<p className="station-refresh-status" role="status">{frame?'Não foi possível atualizar. A mostrar o último quadro recebido. ':''}{errorText(error)}</p>}
 </div>;
}
function stationCoverage(coverage:api.PopupCoverage,calls:api.StopCallPage|undefined,now:number):api.PopupCoverage{
 if(!calls||coverage.status!=='partial'||!calls.data.some(call=>call.arrival.kind==='prediction'||call.departure.kind==='prediction'))return coverage;
 const predictions=calls.data.flatMap(call=>[call.arrival.kind==='prediction'?call.arrival.prediction:null,call.departure.kind==='prediction'?call.departure.prediction:null]).filter(value=>value!=null);
 if(!predictions.length||predictions.some(value=>Date.parse(value.at)>=now&&!!value.valid_until&&Date.parse(value.valid_until)>now))return coverage;
 return {...coverage,status:'partial',message:'Previsões anteriores expiradas; próximas chegadas e partidas por confirmar. Tempos reais anteriores sem fonte comprovada.'};
}
export function VehiclePopup({vehicle,onResolved,onStop}:{vehicle:api.Vehicle,onResolved?:(resolved:boolean)=>void,onStop?:(call:api.VehicleCall)=>void}){
 const now=useClock();const [offset,setOffset]=useState<number>(),navigationRevision=useRef<string|undefined>(undefined),focus=useRef<HTMLElement>(null),lastJourney=useRef<string|null>(null);
 const journey=useQuery({queryKey:['vehicle-journey',vehicle.id,offset],queryFn:async({signal})=>{
  const revision=navigationRevision.current;navigationRevision.current=undefined;
  try{const data=await api.getVehicleJourney(vehicle.id,{limit:100,offset,revision},options(signal));if(!data.association||!data.coverage||!data.page||!Array.isArray(data.data))throw new Error('Resposta de percurso indisponível.');return data}
  catch(error){if((error as {status?:number}).status===410){setOffset(undefined);return api.getVehicleJourney(vehicle.id,{limit:100},options(signal))}throw error}
 },refetchInterval:5000,retry:false});
 const data=journey.data;
 const routeOnly=data?.association==='published_route';
 useEffect(()=>{onResolved?.(data?.association==='resolved'||data?.association==='published_route')},[data?.association,onResolved]);
 const progressFresh=!vehicle.last_known&&!vehicle.stale&&now-Date.parse(vehicle.observed_at)<90000;
 useEffect(()=>{if(!data)return;if(lastJourney.current!==data.journey_id){lastJourney.current=data.journey_id;setOffset(undefined)}},[data?.journey_id]);
 useEffect(()=>{if(data?.next_index!=null&&offset===undefined)focus.current?.scrollIntoView({block:'nearest'})},[data?.journey_id,data?.next_index,offset]);
 function go(to:number){navigationRevision.current=data?.page.revision??undefined;setOffset(to)}
 return <section className={`transit-popup vehicle-journey${vehicle.operator_id==='cp'?' cp-calls':''}`} aria-busy={journey.isFetching}>
  <h4>{routeOnly?'Percurso da linha':'Percurso do veículo'} {data?.direction&&<span>→ {passengerName(vehicle.operator_id,data.direction)}</span>}</h4>
  {journey.error&&<p className="error" role="alert">{errorText(journey.error)}</p>}
  {!data&&journey.isPending&&<p role="status">A carregar percurso…</p>}
  {data&&<><Coverage coverage={data.coverage}/>{data.message&&!(vehicle.operator_id==='metro'&&data.association==='unresolved')&&<p className="notice">{data.message}</p>}
   {(data.association==='resolved'||routeOnly)&&<>
    {data.destination&&normalize(data.destination)!==normalize(data.direction)&&<p>Destino desta viagem: <strong>{passengerName(vehicle.operator_id,data.destination)}</strong></p>}
    <p className="subtle">{data.complete?'Percurso completo':'Percurso parcial'}{routeOnly?' publicado · ligação estimada ao comboio':''} · {data.page.total} visitas · {!progressFresh||data.progress==='unknown'?'Progresso não confirmado':data.progress==='estimated'?'Progresso estimado':'Progresso publicado pelo operador'}</p>
    <div className="journey-actions"><button disabled={!data.page.offset} onClick={()=>go(0)}>Ver início</button><button disabled={data.next_index==null||!progressFresh} onClick={()=>{if(data.next_index!=null){const target=Math.floor(data.next_index/100)*100;if(target!==data.page.offset)go(target);else focus.current?.scrollIntoView({block:'nearest'})}}}>Ver próxima paragem</button></div>
    {!routeOnly&&<div className="transit-call call-head"><span>Paragem</span><span>Chegada</span><span>Partida</span></div>}
    <ol className="journey-timeline" start={data.page.offset+1}>{data.data.map((call,index)=>{const next=progressFresh&&data.next_index===data.page.offset+index;return <li key={call.id} className={`visit-${call.phase}${next?' visit-next':''}`}><article ref={next?focus:undefined}><div className="visit-label">{next?data.progress==='estimated'?'Próxima paragem estimada':'Próxima paragem':call.phase==='previous'?'Paragem anterior':call.phase==='future'?'Por percorrer':call.phase==='current'?progressFresh?'Paragem atual':'Última paragem indicada':'Progresso por confirmar'}</div><CallRow call={call} operator={vehicle.operator_id} now={now} onStop={onStop} routeOnly={routeOnly}/></article></li>})}</ol>
    <div className="pagination"><button disabled={!data.page.offset} onClick={()=>go(Math.max(0,data.page.offset-100))}>Anterior</button><span>{data.page.offset+1}–{data.page.offset+data.data.length} de {data.page.total}</span><button disabled={!data.page.has_more} onClick={()=>go(data.page.offset+data.page.limit)}>Seguinte</button></div>
   </>}
  </>}
 </section>;
}
