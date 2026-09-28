import {positionDeadline,positionIsOld} from './vehicleFreshness';
import {reportingLabel,reportingNotice} from './vehicleReporting';
import {useEffect,useRef,useState} from 'react';
import {useQuery} from '@tanstack/react-query';
import {AlertTriangle,BusFront,Ship,TramFront,TrainFront,X} from 'lucide-react';
import * as api from './api';
import {MetroJourneyPopup} from './MetroPopups';
import {VehiclePopup as JourneyPopup} from './TransitPopups';
import {VehicleSpecifications} from './VehicleSpecifications';
import {errorText,number,observationAge,observationTime,passengerName,plate,routeName,sameVehicleService,stopStatus,time} from './data';

export type SelectedVehiclePath={vehicleId:string,patternId:string,shape:api.RouteShape};
type Props={onShowPath:(show:boolean)=>void,showPath:boolean,onPath:(path:SelectedVehiclePath|undefined)=>void,vehicle:api.Vehicle,mode:string,operatorName:string,now:number,onClose:()=>void,onStop:(call:api.VehicleCall)=>void,filterNotice:string,navigationBusy?:boolean,navigationError?:string};
const notices:Record<string,string>={partial:'As previsões têm cobertura parcial.',plan_mismatch:'O plano publicado mudou; o percurso deste registo não pode ser associado.',unidentified_service:'Não foi possível identificar com segurança o percurso deste serviço.',next_stop_only:'Percurso completo indisponível; mostramos a próxima paragem publicada.',unavailable:'Paragens deste serviço indisponíveis.'};
const propulsion:Record<string,string>={diesel:'Diesel',electricity:'Elétrica',natural_gas:'Gás natural',hydrogen:'Hidrogénio',gasoline:'Gasolina'};
const publishedText=(raw:string|undefined|null)=>{const value=raw?.trim();return value?propulsion[value]??(/^[0-9]+$/.test(value)?`Código publicado: ${value}`:value):undefined};

export default function VehiclePopup({vehicle:v,mode,operatorName,now,onClose,onStop,filterNotice,navigationBusy,navigationError,showPath,onPath,onShowPath}:Props){
 const [journeyResolved,setJourneyResolved]=useState(false);
 const origin=useRef(v),warningsRef=useRef<HTMLElement>(null),bound=useRef({observed:v.observed_at,stop:v.stop_id,status:v.current_status,previous:!!v.last_known||!!v.stale,at:now});
 const [reference,setReference]=useState(v.vehicle_ref?.reference),[offset,setOffset]=useState(0),[revision,setRevision]=useState<string>();
 const [path,setPath]=useState<{reference:string,shape:api.RouteShape}>();
 const includeGeometry=v.operator_id==='cm'&&!!v.pattern_id&&showPath&&offset===0;
 const calls=useQuery({queryKey:['vehicle-calls',v.id,reference,offset,revision,includeGeometry],enabled:!!reference&&v.operator_id!=='metro',staleTime:30000,gcTime:60000,retry:false,queryFn:async({signal})=>{
  const result=await api.getVehicleCalls(v.id,{reference,limit:20,offset,revision,includeGeometry},{signal});
  if(!result.vehicle||!sameVehicleService(result.vehicle,origin.current))throw new Error('Esta ligação já não identifica o mesmo serviço.');
  return result;
 }});
 const needsGeometry=!!reference&&!!v.pattern_id&&v.operator_id==='cm'&&showPath&&offset>0&&path?.reference!==reference;
 const geometry=useQuery({queryKey:['vehicle-geometry',v.id,reference,revision,showPath],enabled:needsGeometry,staleTime:30000,gcTime:60000,retry:false,queryFn:async({signal})=>{
  const result=await api.getVehicleCalls(v.id,{reference,revision,limit:20,offset:0,includeGeometry:true},{signal});
  if(!sameVehicleService(result.vehicle,origin.current))throw new Error('Esta ligação já não identifica o mesmo serviço.');
  return result;
 }});
 const geometryError=needsGeometry?geometry.error:undefined;
 const responseShape=offset===0?calls.data?.geometry:geometry.data?.geometry;
 useEffect(()=>{if(showPath&&responseShape&&reference&&!calls.error&&!geometryError)setPath({reference,shape:responseShape})},[responseShape,reference,calls.error,geometryError,showPath]);
 useEffect(()=>{
  const visible=showPath&&!calls.error&&!geometryError&&reference===path?.reference&&v.pattern_id&&now<positionDeadline(v);
  onPath(visible&&path?{vehicleId:v.id,patternId:v.pattern_id!,shape:path.shape}:undefined);
 },[showPath,path,reference,v.id,v.pattern_id,calls.error,geometryError,now>=positionDeadline(v),onPath]);
 useEffect(()=>()=>onPath(undefined),[onPath]);
 useEffect(()=>{
  const changed=bound.current.observed!==v.observed_at||bound.current.stop!==v.stop_id||bound.current.status!==v.current_status||bound.current.previous!==(!!v.last_known||!!v.stale);
  if(offset===0&&v.vehicle_ref&&(changed||now-bound.current.at>=30000)&&v.vehicle_ref.reference!==reference){setReference(v.vehicle_ref.reference);setRevision(undefined);bound.current={observed:v.observed_at,stop:v.stop_id,status:v.current_status,previous:!!v.last_known||!!v.stale,at:now}}
 },[v.observed_at,v.stop_id,v.current_status,v.last_known,v.stale,v.vehicle_ref?.reference,now,offset,reference]);
 const reload=()=>{if(!sameVehicleService(v,origin.current))return;setOffset(0);setRevision(undefined);if(v.vehicle_ref?.reference!==reference)setReference(v.vehicle_ref?.reference);else void calls.refetch()};
 const predictionsExpired=!!calls.data?.valid_until&&Date.parse(calls.data.valid_until)<=now;
 useEffect(()=>{if(predictionsExpired&&offset===0)reload()},[predictionsExpired,v.vehicle_ref?.reference]);
 const previous=v.last_known||v.stale||now-Date.parse(v.observed_at)>180000;
 const expired=now>=positionDeadline(v);
 const observedTime=observationTime(v.observed_at,now),collectedTime=observationTime(v.collected_at,now);
 const warnings:string[]=[];
 const signalNotice=reportingNotice(v);
 if(signalNotice)warnings.push(signalNotice);
 else if(previous)warnings.push('A aguardar atualização; mostramos o último registo conhecido.');
 if(observationAge(v.observed_at,now)==='hora não confirmada')warnings.push('A hora da observação não pôde ser confirmada.');
 if(expired)warnings.push('Sinal expirado. Esta observação deixou de estar disponível no mapa.');
 if(positionIsOld(v,now)&&!expired)warnings.push(`Sem atualização há pelo menos 5 minutos · ${observationAge(v.observed_at,now)}.`);
 if(v.position_kind==='estimated'&&v.operator_id!=='metro')warnings.push('A posição é estimada pela fonte; não representa uma observação GPS. A velocidade amostral não é suportada para estas posições.');
 if(calls.data&&notices[calls.data.availability]&&!(v.operator_id==='metro'&&calls.data.availability==='unidentified_service'))warnings.push(notices[calls.data.availability]);
 if(v.operator_id==='cp'&&calls.data?.availability==='available'&&!calls.data.data.some(row=>row.kind==='predicted'))warnings.push('Sem previsões verificadas para este serviço; mostramos o horário planeado.');
 if(calls.error)warnings.push(errorText(calls.error));
 if(geometryError)warnings.push(`Percurso no mapa indisponível: ${errorText(geometryError)}`);
 if(predictionsExpired)warnings.push('As previsões expiraram. Atualize o percurso para consultar dados válidos.');
 if(filterNotice)warnings.push(filterNotice);
 if(navigationError)warnings.push(navigationError);
 const unique=[...new Set(warnings)];
 const jump=()=>{const footer=warningsRef.current,panel=footer?.closest<HTMLElement>('.detail-panel');if(!footer||!panel)return;panel.scrollTo({top:panel.scrollTop+footer.getBoundingClientRect().top-panel.getBoundingClientRect().top-12,behavior:matchMedia('(prefers-reduced-motion: reduce)').matches?'instant':'smooth'});footer.focus({preventScroll:true})};
 const Icon=mode==='metro'?TramFront:mode==='train'?TrainFront:mode==='ferry'?Ship:BusFront;
 const fields:[string,string][]=[];
 if(v.route_name?.trim())fields.push([mode==='bus'?'Carreira':'Linha',routeName(v.operator_id,v.route_name)]);
 for(const [label,value] of [['Tipologia',publishedText(v.typology)],['Propulsão',publishedText(v.propulsion)],['Modelo',v.model?.trim()],['Matrícula',v.license_plate?.trim()?plate(v.license_plate,mode):undefined]] as const)if(value)fields.push([label,value]);
 if(!previous&&v.position_kind==='reported'&&v.speed_kmh!=null)fields.push(['Velocidade amostral',`${number(v.speed_kmh)} km/h`]);
 const state=v.current_status?stopStatus({...v,last_known:previous}):previous?'Última posição conhecida':'';
 const rows=predictionsExpired?[]:calls.data?.data??[];
 const predicted=rows.some(row=>row.kind==='predicted');
 const frozen=!!calls.data&&(calls.data.vehicle.observed_at!==v.observed_at||calls.data.vehicle.stop_id!==v.stop_id||calls.data.vehicle.last_known!==v.last_known);
 return <>
  <div className="section-head vehicle-heading"><h3><Icon size={20}/>{v.operator_id==='metro'?`Comboio ${v.source_id}`:v.service_label?`${mode==='train'?'Comboio':'Serviço'} ${v.service_label}`:`${operatorName} · ${v.source_id}`}</h3><div>{unique.length>0&&<button className="quiet warning-jump" onClick={jump} aria-label={`Ver ${unique.length} avisos`}><AlertTriangle size={18}/><span>{unique.length}</span></button>}<button className="quiet" aria-label="Fechar detalhes" onClick={onClose}><X size={18}/></button></div></div>
  {v.position_kind==='estimated'&&v.operator_id!=='metro'&&<span className="pill estimated">Posição estimada</span>}
  {v.reporting&&<p className="vehicle-reporting">{reportingLabel(v)}</p>}
  {state&&<p className="vehicle-status">{state.replace('Último estado:','Último registo:')}</p>}
  <p className="subtle vehicle-update-age">Última atualização do veículo: <time dateTime={v.observed_at} title={Number.isFinite(Date.parse(v.observed_at))?new Date(v.observed_at).toLocaleString('pt-PT',{timeZone:'Europe/Lisbon'}):'Hora não confirmada'}>{observedTime}</time></p>
  {collectedTime!==observedTime&&<p className="subtle">Recebido pela aplicação: <time dateTime={v.collected_at}>{collectedTime}</time></p>}
  <>
   {v.scheduled_service&&<p className="journey">{passengerName(v.operator_id,v.scheduled_service.origin_name)} → {passengerName(v.operator_id,v.scheduled_service.destination_name)} <small>Serviço planeado</small></p>}
   {fields.length>0&&<div className="detail-grid">{fields.map(([label,value])=><span key={label}>{label}<strong>{value}</strong></span>)}</div>}
   <VehicleSpecifications vehicle={v}/>
   {v.operator_id==='metro'?<MetroJourneyPopup vehicle={v}/>:<JourneyPopup vehicle={v} onResolved={setJourneyResolved} onStop={onStop}/>}{reference&&v.operator_id!=='metro'&&!(v.operator_id==='metro'&&journeyResolved)&&<section className="vehicle-calls" aria-busy={calls.isFetching||geometry.isFetching}><h4>{calls.data?.coverage==='complete_published_route'?'Percurso completo publicado':calls.data?.availability==='next_stop_only'?'Próxima paragem publicada':v.operator_id==='cm'?'Paragens publicadas':previous||frozen?'Percurso do último serviço observado':predicted?'Próximas paragens previstas':calls.data?.progress==='known'?'Próximas paragens planeadas':'Percurso planeado na área de Lisboa'}</h4>
    {v.operator_id==='cm'&&v.pattern_id&&<label className="path-toggle"><input type="checkbox" checked={showPath} onChange={e=>onShowPath(e.target.checked)}/>Mostrar percursos de autocarro no mapa</label>}
    {frozen&&calls.data&&<p className="subtle">Paragens associadas ao registo de {observationTime(calls.data.vehicle.observed_at,now)}. <button className="quiet" onClick={reload}>Atualizar percurso</button></p>}
    {navigationBusy&&<p role="status">A abrir paragem…</p>}
    {calls.isFetching&&<p role="status">A carregar paragens…</p>}{geometry.isFetching&&<p role="status">A carregar percurso no mapa…</p>}
    {!journeyResolved&&rows.map(row=><div className="arrival" key={row.id}>{row.stop&&row.stop_static_updated_at?<button className="stop-link" disabled={navigationBusy} onClick={()=>onStop(row)}>{passengerName(v.operator_id,row.stop_name)}</button>:<strong>{row.stop?passengerName(v.operator_id,row.stop_name):'Paragem não identificada'}</strong>}{(row.expected_at||row.scheduled_at)&&<time>{time(row.expected_at??row.scheduled_at)}</time>}<small>{row.kind==='predicted'?'Previsão':row.kind==='scheduled'?'Horário planeado':'Paragem publicada'}{row.delay_seconds!=null?` · desvio ${row.delay_seconds>0?'+':''}${number(row.delay_seconds/60)} min`:''}</small></div>)}
    {!journeyResolved&&!calls.isFetching&&!rows.length&&!calls.error&&<p className="empty">Sem paragens disponíveis neste registo.</p>}
    {!journeyResolved&&calls.data&&<div className="pagination"><button disabled={offset===0||calls.isFetching} onClick={()=>{setRevision(calls.data?.page.revision??undefined);setOffset(Math.max(0,offset-20))}}>Anterior</button><span>{calls.data.page.total} paragens</span><button disabled={!calls.data.page.has_more||calls.isFetching} onClick={()=>{setRevision(calls.data?.page.revision??undefined);setOffset(offset+20)}}>Seguinte</button></div>}
   </section>}
  </>
  <section ref={warningsRef} className="popup-footnotes" tabIndex={-1} aria-label="Avisos e fontes">{unique.map(w=><p className="notice" key={w}><AlertTriangle size={14}/>{w}</p>)}{(calls.error||geometryError||predictionsExpired)&&<button onClick={reload}>Atualizar percurso</button>}
   {calls.data&&<p className="subtle">{calls.data.coverage==='complete_published_route'?'Todas as paragens da variante publicada, sem indicar quais já foram percorridas.':'Mostramos as paragens publicadas disponíveis na área de Lisboa.'} Horários, previsões e percursos não alteram a posição nem a hora da observação.</p>}
   {(v.seated_capacity!=null||v.total_capacity!=null||v.wheelchair_accessible!=null||v.contactless!=null)&&<p className="subtle">Características publicadas, não lugares livres. “Não indicado” pode corresponder a omissão na origem.</p>}
   {v.service_label&&v.source_id!==v.service_label&&<p className="subtle">Referência da fonte: {v.source_id}</p>}
   {v.reporting&&!v.reporting.persisted&&<p className="subtle">O estado de sinal apresentado ainda aguarda gravação.</p>}
   <a href={v.source_url} target="_blank" rel="noreferrer">Fonte · {operatorName}</a>
  </section>
 </>;
}
