import {HttpError} from '@oazapfts/runtime';
import type {ArrivalPage,Vehicle} from './api';
import {errorText,passengerName,time} from './data';

type Props={page?:ArrivalPage,pending:boolean,error:unknown,now:number,operatorNames:Record<string,string>,vehicles:Vehicle[],onVehicle:(v:Vehicle)=>void};
export function StopArrivals({page,pending,error,now,operatorNames,vehicles,onVehicle}:Props){
 const invalidated=error instanceof HttpError&&(error.status===404||error.status===410);
 const rows=invalidated?[]:page?.data.filter(a=>{const at=a.expected_at??a.scheduled_at;return !!at&&Date.parse(at)>=now&&(!a.valid_until||Date.parse(a.valid_until)>now)})??[];
 const expired=page?.data.some(a=>a.kind==='prediction'&&a.valid_until&&Date.parse(a.valid_until)<=now);
 const message=invalidated?undefined:expired&&!rows.some(a=>a.kind==='prediction')?'Dados antigos. Previsões atuais indisponíveis.':page?.data.length&&!rows.length&&page.availability?.status==='ok'?'Sem próximas passagens publicadas nesta recolha.':page?.availability?.message;
 return <><h4>Próximas chegadas</h4>{rows.map(a=>{
  const vehicle=a.vehicle_id?vehicles.find(v=>v.id===a.vehicle_id&&!v.last_known&&!v.stale&&!!a.plan_id&&v.plan_id===a.plan_id&&!!a.service_date&&v.operational_date===a.service_date&&v.trip_id===a.trip_id):undefined;
  return <div className="arrival" key={a.id}><strong>{passengerName(a.operator_id,a.route_name,'Linha indisponível')}</strong><span>{passengerName(a.operator_id,a.headsign,'Destino indisponível')}</span><time>{time(a.expected_at??a.scheduled_at)}</time><small>{a.kind==='prediction'?`Previsão · ${operatorNames[a.operator_id]??'fornecedor'}`:'Horário planeado'}</small>{vehicle&&<button onClick={()=>onVehicle(vehicle)}>Ver veículo</button>}</div>
 })}{pending&&<div className="empty">A carregar próximas passagens…</div>}{message&&<p className="notice" role="status">{message}</p>}{!invalidated&&page?.availability?.source_updated_at&&<small>Atualização da fonte: {time(page.availability.source_updated_at)}</small>}{!pending&&!rows.length&&!message&&!error&&<div className="empty">Próximas passagens indisponíveis.</div>}{!!error&&<p className="error">{errorText(error)}</p>}</>
}
