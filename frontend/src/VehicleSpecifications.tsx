import type {Vehicle} from './api';
import {number} from './data';
const equipment=(value:boolean)=>value?'Sim (publicado)':'Não indicado pela fonte';
export function VehicleSpecifications({vehicle}:{vehicle:Vehicle}){
 const fields:[string,string][]=[];
 if(vehicle.seated_capacity!=null)fields.push(['Lugares sentados',number(vehicle.seated_capacity,0)]);
 if(vehicle.total_capacity!=null)fields.push(['Capacidade total',number(vehicle.total_capacity,0)]);
 if(vehicle.wheelchair_accessible!=null)fields.push(['Acessibilidade',equipment(vehicle.wheelchair_accessible)]);
 if(vehicle.contactless!=null)fields.push(['Contactless',equipment(vehicle.contactless)]);
 return fields.length?<div className="detail-grid specifications">{fields.map(([label,value])=><span key={label}>{label}<strong>{value}</strong></span>)}</div>:null;
}
