import type {Arrival,CpPrediction,CpPredictionPage,VehicleReference} from './api';
import {currentCPPredictions,predictionMatchesArrival} from './cp';
import {passengerName,number,routeName,time} from './data';

type Navigate=(reference:VehicleReference)=>void;
function ServiceTitle({operator,label,route}:{operator:string,label?:string|null,route?:string|null}){return <strong>{label?`${operator==='cp'||operator==='fertagus'?'Comboio':'Serviço'} ${label}`:routeName(operator,route)||'Serviço publicado'}</strong>}
export function ArrivalRow({row,onVehicle}:{row:Arrival,onVehicle?:Navigate}){
 const content=<><ServiceTitle operator={row.operator_id} label={row.service_label} route={row.route_name}/><span>{passengerName(row.operator_id,row.headsign,'Destino não publicado')}</span><time>{time(row.expected_at??row.scheduled_at)}</time><small>{row.kind==='prediction'?'Previsão':'Horário planeado'}{row.vehicle_ref?' · Ver veículo':''}</small></>;
 return row.vehicle_ref&&onVehicle?<button className="arrival vehicle-link" onClick={()=>onVehicle(row.vehicle_ref!)}>{content}</button>:<div className="arrival">{content}</div>;
}
function Prediction({row,onVehicle}:{row:CpPrediction,onVehicle?:Navigate}){
 const content=<><ServiceTitle operator="cp" label={row.service_label} route={row.route_name}/><span>{passengerName('cp',row.destination_name,'Destino não publicado')}</span><time>{time(row.expected_at)}</time><small>Previsão TML · CP{row.scheduled_at?` · planeado ${time(row.scheduled_at)}`:''}{row.vehicle_ref?' · Ver veículo':''}</small>{row.delay_seconds!=null&&<small>Desvio previsto: {row.delay_seconds>0?'+':''}{number(row.delay_seconds/60,1)} min{row.delay_seconds<0?' (adiantado)':''}</small>}<small>{routeName('cp',row.route_name)}</small></>;
 return row.vehicle_ref&&onVehicle?<button className="arrival cp-arrival vehicle-link" data-testid="cp-prediction" onClick={()=>onVehicle(row.vehicle_ref!)}>{content}</button>:<div className="arrival cp-arrival" data-testid="cp-prediction">{content}</div>;
}
export function CPStationArrivals({page,planned,now,onVehicle}:{page:CpPredictionPage,planned:Arrival[],now:number,onVehicle?:Navigate}){
 const predictions=currentCPPredictions(page.data,now);
 const schedule=planned.filter(a=>a.scheduled_at&&Date.parse(a.scheduled_at)>=now&&!predictions.some(p=>predictionMatchesArrival(p,a)));
 const rows=[...predictions.map(p=>({at:Date.parse(p.expected_at),id:p.id,p})),...schedule.map(a=>({at:Date.parse(a.scheduled_at!),id:a.id,a}))].sort((a,b)=>a.at-b.at||a.id.localeCompare(b.id));
 const expired=page.data.length>0&&!predictions.length;
 return <><h4>Próximas chegadas</h4>{rows.slice(0,20).map(row=>'p' in row?<Prediction key={row.id} row={row.p} onVehicle={onVehicle}/>:<ArrivalRow key={row.id} row={row.a} onVehicle={onVehicle}/>)}{!rows.length&&<p className="empty">Sem chegadas disponíveis neste intervalo.</p>}{rows.length>20&&<small>Mostramos as próximas 20 chegadas.</small>}<div className="popup-footnotes">{(expired||page.availability.status!=='ok')&&<p className="notice" role="status">{expired?'Previsões expiradas; mostramos os horários planeados.':page.availability.message}</p>}{rows.some(r=>'p' in r?!r.p.vehicle_ref:!r.a.vehicle_ref)&&<p className="subtle">Alguns serviços não têm um veículo identificado com segurança no mapa.</p>}<p className="subtle">Previsões de chegada à estação; não representam posições GPS nem pontualidade medida.</p></div></>;
}
