import type {Vehicle} from './api';
import {number} from './data';
const equipment=(value:boolean|null|undefined)=>value==null?'Indisponível':value?'Sim (publicado)':'Não indicado';
export function VehicleSpecifications({vehicle}:{vehicle:Vehicle}){
 return <><div className="detail-grid"><span>Lugares sentados (capacidade publicada)<strong>{vehicle.seated_capacity==null?'Indisponível':number(vehicle.seated_capacity,0)}</strong></span><span>Capacidade total publicada<strong>{vehicle.total_capacity==null?'Indisponível':number(vehicle.total_capacity,0)}</strong></span><span>Acessibilidade para cadeira de rodas (publicada)<strong>{equipment(vehicle.wheelchair_accessible)}</strong></span><span>Pagamento contactless (publicado)<strong>{equipment(vehicle.contactless)}</strong></span></div><p className="subtle">Características publicadas do veículo. Não indicam lugares livres nem confirmam disponibilidade efetiva do equipamento. “Não indicado” pode corresponder a omissão na origem.</p></>;
}
